# -*- coding: utf-8 -*-
"""把官方 AngelSlim 1.25bit GGUF（type 40, scale=int16）转换为 PR #22836 的 STQ1_0 格式（type 43, scale=f16）。

原理：两者 42B/256 块布局一致（qs[32]+sign[8]+scale[2]），仅 scale 编码不同：
  - 原始: int16，weight = lane * (int16 / 32768)
  - 目标: f16，  weight = lane * d_f16
故 d_f16 = f16(int16/32768)（或 f16(|int16|/32768)）。
同时把张量类型 ID 40 改写为 43（STQ1_0），其余字节保持不变。
"""
import struct, sys, os
import numpy as np

MODE = sys.argv[1] if len(sys.argv) > 1 else "preserve"  # preserve | abs
SRC = sys.argv[2] if len(sys.argv) > 2 else "models/Hy-MT2-1.8B-1.25bit-v2.gguf"
DST = sys.argv[3] if len(sys.argv) > 3 else "models/Hy-MT2-1.8B-1.25bit-STQ1_0.gguf"

BLOCK_BYTES = 42
QK_K = 256

def read_str(b, o):
    l = struct.unpack_from("<Q", b, o)[0]; o += 8
    return b[o:o+l].decode("utf-8", "replace"), o + l

def skiptype(b, o, t):
    if t in (0,1,7): return o+1
    if t in (2,3): return o+2
    if t in (4,5,6): return o+4
    if t == 8:
        _, o = read_str(b, o); return o
    if t == 9:
        et = struct.unpack_from("<I", b, o)[0]; o += 4
        n = struct.unpack_from("<Q", b, o)[0]; o += 8
        for _ in range(n): o = skiptype(b, o, et)
        return o
    if t in (10,11,12): return o+8
    raise ValueError("unknown kv type %d" % t)

data = bytearray(open(SRC, "rb").read())
assert data[0:4] == b"GGUF", "not gguf"
off = 4
ver = struct.unpack_from("<I", data, off)[0]; off += 4
n_t = struct.unpack_from("<Q", data, off)[0]; off += 8
n_kv = struct.unpack_from("<Q", data, off)[0]; off += 8
for _ in range(n_kv):
    _, off = read_str(data, off)
    t = struct.unpack_from("<I", data, off)[0]; off += 4
    off = skiptype(data, off, t)

# 收集 type-40 张量
tensors = []
for i in range(n_t):
    name, off = read_str(data, off)
    nd = struct.unpack_from("<I", data, off)[0]; off += 4
    dims = [struct.unpack_from("<q", data, off+8*j)[0] for j in range(nd)]
    off += 8*nd
    ttype_pos = off
    ttype = struct.unpack_from("<I", data, off)[0]; off += 4
    toff = struct.unpack_from("<Q", data, off)[0]; off += 8
    if ttype == 40:
        ne = 1
        for d in dims: ne *= d
        tensors.append((name, ne, toff, ttype_pos))

print("type-40 张量数:", len(tensors))
total_blocks = sum(ne // QK_K for _, ne, _, _ in tensors)
print("总块数:", total_blocks)

# 逐张量转换：类型 40->43，scale int16->f16
converted = 0
i16 = np.dtype('<i2')
for name, ne, toff, tpos in tensors:
    struct.pack_into('<I', data, tpos, 43)  # type ID -> STQ1_0
    nblk = ne // QK_K
    nbytes = nblk * BLOCK_BYTES
    region = np.frombuffer(data, dtype=np.uint8, count=nbytes, offset=toff)
    # 提取每块末尾 2 字节作为 int16
    block_view = region.reshape(nblk, BLOCK_BYTES)
    raw = block_view[:, 40:42].copy().view(i16).reshape(-1)
    if MODE == "abs":
        vals = np.abs(raw).astype(np.float32) / 32768.0
    else:
        vals = raw.astype(np.float32) / 32768.0
    f16 = vals.astype(np.float16)
    block_view[:, 40:42] = f16.view(np.uint8).reshape(-1, 2)
    converted += nblk

print("已转换块数:", converted)
open(DST, "wb").write(data)
print("已保存:", DST, os.path.getsize(DST), "bytes (mode=%s)" % MODE)
