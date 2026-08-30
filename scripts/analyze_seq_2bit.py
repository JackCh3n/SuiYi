# -*- coding: utf-8 -*-
"""判别 SEQ 2-bit 块布局: 256w/65B vs 512w/130B, 并验证 2bit 码与 scale 解码"""
import struct, sys
from collections import Counter

def read_str(f):
    ln = struct.unpack("<Q", f.read(8))[0]
    return f.read(ln).decode("utf-8", errors="replace")

def skip_value(f, vtype):
    if vtype in (0, 1, 7): f.read(1)
    elif vtype in (2, 3): f.read(2)
    elif vtype in (4, 5, 6): f.read(4)
    elif vtype == 8: read_str(f)
    elif vtype == 9:
        atype = struct.unpack("<I", f.read(4))[0]
        cnt = struct.unpack("<Q", f.read(8))[0]
        for _ in range(cnt): skip_value(f, atype)
    elif vtype in (10, 11, 12): f.read(8)

def decode_2bit(data_bytes, level_map, n):
    """把 n 个 2bit 码从字节流解出, level_map: {0,1,2,3}->level"""
    out = []
    for i in range(n):
        b = data_bytes[i >> 2]
        code = (b >> (2 * (i & 3))) & 0x3
        out.append(level_map[code])
    return out

LEVELS_A = [-1.5, -0.5, 0.5, 1.5]   # 假设 code0=-1.5 ... code3=+1.5
LEVELS_B = [-3.0, -1.0, 1.0, 3.0]   # 假设 code0=-3 ... code3=+3 (d/2)

path = sys.argv[1]
with open(path, "rb") as f:
    assert f.read(4) == b"GGUF"
    version = struct.unpack("<I", f.read(4))[0]
    n_tensors = struct.unpack("<Q", f.read(8))[0]
    n_kv = struct.unpack("<Q", f.read(8))[0]
    alignment = 32
    for _ in range(n_kv):
        key = read_str(f)
        t = struct.unpack("<I", f.read(4))[0]
        if key == "general.alignment" and t == 4:
            alignment = struct.unpack("<I", f.read(4))[0]
        else:
            skip_value(f, t)
    tensors = []
    for i in range(n_tensors):
        name = read_str(f)
        nd = struct.unpack("<I", f.read(4))[0]
        dims = [struct.unpack("<q", f.read(8))[0] for _ in range(nd)]
        ttype = struct.unpack("<I", f.read(4))[0]
        off = struct.unpack("<Q", f.read(8))[0]
        tensors.append((name, dims, ttype, off))
    tensors.sort(key=lambda t: t[3])
    # 第一个 type-41 张量
    t41 = [t for t in tensors if t[2] == 41]
    name, dims, ttype, off = t41[0]
    nxt = tensors[tensors.index(t41[0])+1][3]
    size = nxt - off
    ne = 1
    for d in dims: ne *= d
    print("tensor:", name, "dims:", dims, "ne:", ne, "size:", size)
    f.seek(off)
    data = f.read(size)

def test_hyp(blk_w, bpb, scale_kind, data, nblk):
    print("\n==== 假设: %d 权重/块, %d 字节/块, scale=%s ====" % (blk_w, bpb, scale_kind))
    okA = okB = 0
    totA = totB = 0
    codefreq = Counter()
    scale_samples = []
    bad_ratio = []
    for i in range(min(nblk, 512)):
        blk = data[i*bpb:(i+1)*bpb]
        if scale_kind == "f16_end":
            d = struct.unpack("<e", blk[bpb-2:bpb])[0]
            code_bytes = blk[:bpb-2]
            if d == 0: continue
        elif scale_kind == "u8_end":
            d = blk[bpb-1] / 128.0   # 猜测: 1字节 scale 归一化
            code_bytes = blk[:bpb-1]
            if d == 0: continue
        n = blk_w
        for code in decode_2bit(code_bytes, [0,1,2,3], n):
            codefreq[code] += 1
        # 用两种 level 映射分别求 weight/d 的集合
        valsA = [lv * d for lv in decode_2bit(code_bytes, LEVELS_A, n)]
        valsB = [lv * (d/2) for lv in decode_2bit(code_bytes, LEVELS_B, n)]
        sa = set(round(v/d, 6) for v in valsA if abs(v/d) > 1e-9)
        sb = set(round(v/d, 6) for v in valsB if abs(v/d) > 1e-9)
        # 期望: A -> {±0.5, ±1.5}; B -> {±0.5, ±1.5} 当 d 减半时其实一样...
        # 用比值判定: max|level|/min|level| 应为 3
        ma = max(abs(x) for x in sa); mi = min(abs(x) for x in sa)
        mb = max(abs(x) for x in sb); mi2 = min(abs(x) for x in sb)
        if ma/mi > 2.5 and ma/mi < 3.5: okA += 1
        if mb/mi2 > 2.5 and mb/mi2 < 3.5: okB += 1
        totA += 1; totB += 1
        if i < 5: scale_samples.append((i, d, sorted(sa)[:6]))
        if not (2.5 < ma/mi < 3.5):
            bad_ratio.append((i, d, sorted(sa)[:6]))
    print("2bit 码频次:", codefreq.most_common(8))
    print("前5块 scale=%s, 归一化|level|集合:%s" % (scale_samples[:3], [s[2] for s in scale_samples[:3]]))
    print("比值≈3 的块占比: 映射A=%.1f%%  映射B=%.1f%%" % (100.0*okA/totA, 100.0*okB/totB))
    print("异常块样例:", bad_ratio[:3])

nblk = size // 65
# 假设 A: 512w/130B f16_end
test_hyp(512, 130, "f16_end", data, nblk)
# 假设 B: 256w/65B u8_end
test_hyp(256, 65, "u8_end", data, nblk)
# 假设 C: 256w/66B f16_end (64 codes + f16) 检查是否整除吻合
print("\n==== 额外检查 ====")
for bw in (256, 512):
    for extra in (1, 2):
        if (size / (bw/4 + extra)) == size // (bw/4 + extra):
            print("size %d 能被 %d(=%d码+%d)整除 -> %d 块" % (size, bw//4+extra, bw//4, extra, size//(bw//4+extra)))
