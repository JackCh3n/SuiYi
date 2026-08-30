# -*- coding: utf-8 -*-
"""SEQ 布局暴力搜索 v2: 加入 pos1 scale、130B f16 scale 等结构, Q4_K 相关性评分"""
import struct, itertools
import numpy as np

Q4K = r'models/Hy-MT2-1.8B-Q4_K_M.gguf'
SEQ = r'models/Hy-MT2-1.8B-2bit-v2.gguf'

def read_str(f):
    ln = struct.unpack('<Q', f.read(8))[0]
    return f.read(ln).decode('utf-8', 'replace')
def skip(f, t):
    if t in (0, 1, 7): f.read(1)
    elif t in (2, 3): f.read(2)
    elif t in (4, 5, 6): f.read(4)
    elif t == 8: read_str(f)
    elif t == 9:
        et = struct.unpack('<I', f.read(4))[0]; n = struct.unpack('<Q', f.read(8))[0]
        for _ in range(n): skip(f, et)
    elif t in (10, 11, 12): f.read(8)
def find_tensor(path, tname):
    f = open(path, 'rb'); f.read(4); f.read(4); n_t = struct.unpack('<Q', f.read(8))[0]; n_kv = struct.unpack('<Q', f.read(8))[0]
    for _ in range(n_kv):
        read_str(f); skip(f, struct.unpack('<I', f.read(4))[0])
    for i in range(n_t):
        name = read_str(f); nd = struct.unpack('<I', f.read(4))[0]
        dims = [struct.unpack('<q', f.read(8))[0] for _ in range(nd)]
        ttype = struct.unpack('<I', f.read(4))[0]; off = struct.unpack('<Q', f.read(8))[0]
        if name == tname:
            for _ in range(i + 1, n_t):
                read_str(f); nd2 = struct.unpack('<I', f.read(4))[0]
                for _ in range(nd2): f.read(8)
                f.read(4); f.read(8)
            data_off = (f.tell() + 31) // 32 * 32
            f.close(); return dims, ttype, off, data_off
    f.close(); return None

def dequant_q4k(data, n):
    blocks = data.reshape(-1, 144)
    nb = len(blocks)
    d = np.frombuffer(blocks[:, 0:2].tobytes(), dtype='<f2').astype(np.float32)
    dmin = np.frombuffer(blocks[:, 2:4].tobytes(), dtype='<f2').astype(np.float32)
    qs = blocks[:, 16:144]; sc = blocks[:, 4:16]
    out = np.empty(nb * 256, dtype=np.float32)
    for j in range(8):
        if j < 4:
            dl = sc[:, j] & 63; ml = sc[:, j + 4] & 63
        else:
            dl = (sc[:, j + 4] & 0xF) | ((sc[:, j - 4] >> 6) << 4)
            ml = (sc[:, j + 4] >> 4) | ((sc[:, j - 0] >> 6) << 4)
        d_scaled = d * dl.astype(np.float32)
        m_scaled = dmin * ml.astype(np.float32)
        qq = qs[:, j * 16:(j + 1) * 16]
        lo = (qq & 0xF).astype(np.float32); hi = ((qq >> 4) & 0xF).astype(np.float32)
        col = d_scaled[:, None] * np.concatenate([lo, hi], axis=1).reshape(nb, 32) - m_scaled[:, None]
        out.reshape(nb, 256)[:, j * 32:(j + 1) * 32] = col
    return out[:n]

# ---------- SEQ 通用解码 ----------
# 布局描述: 块大小 bs 元素, 码字节数 nb_code, scale 配置
# code_index: 每个码字节在块内的字节偏移 (长度 nb_code)
# order: 'linear' 或 's16'
def seq_imap_256(order):
    if order == 'linear':
        return np.array([[i * 4 + p for p in range(4)] for i in range(64)], dtype=np.int64)
    return np.array([[(i // 16) * 64 + (i % 16) + p * 16 for p in range(4)] for i in range(64)], dtype=np.int64)

def seq_imap_512(order):
    if order == 'linear':
        return np.array([[i * 4 + p for p in range(4)] for i in range(128)], dtype=np.int64)
    # s16: chunk=16*16=256? 尝试两种
    return np.array([[(i // 16) * 64 + (i % 16) + p * 16 for p in range(4)] for i in range(128)], dtype=np.int64)

def decode_seq(data, n, bs, code_off, ncode, scale_fn, order, bits, levels):
    """bs: 块内元素数; code_off: 码区起始字节偏移(块内); ncode: 码字节数;
       scale_fn(blk)->[nb] scale"""
    bb = ncode + (0)  # 块字节数 = ncode + scale字节数
    blocks = data.reshape(-1, bb)
    nb = len(blocks)
    lv = np.array(levels, dtype=np.float32)
    code = blocks[:, code_off:code_off + ncode]
    scale = scale_fn(blocks)
    if bits == 'lsb':
        lanes = [(code & 3), ((code >> 2) & 3), ((code >> 4) & 3), ((code >> 6) & 3)]
    else:
        lanes = [((code >> 6) & 3), ((code >> 4) & 3), ((code >> 2) & 3), (code & 3)]
    vals = [lv[l] for l in lanes]
    imap = seq_imap_256(order) if bs == 256 else seq_imap_512(order)
    out = np.empty(nb * bs, dtype=np.float32)
    outr = out.reshape(nb, bs)
    for p in range(4):
        outr[:, imap[:, p]] = vals[p] * scale[:, None]
    return out[:n]

def pearson(a, b):
    a = a - a.mean(); b = b - b.mean()
    den = np.sqrt((a * a).sum() * (b * b).sum())
    return float((a * b).sum() / den) if den > 0 else 0.0
def sign_agree(a, b):
    return float((np.sign(a) == np.sign(b)).mean())

TENSOR = 'blk.0.attn_k.weight'
dims, ttype, off, data_off = find_tensor(Q4K, TENSOR)
n_elem = dims[0] * dims[1]
with open(Q4K, 'rb') as f:
    f.seek(data_off + off); q4k_raw = np.frombuffer(f.read((n_elem // 256) * 144), dtype=np.uint8)
ref = dequant_q4k(q4k_raw, n_elem)
print(f'{TENSOR} n={n_elem} Q4K ref mean={ref.mean():.4f} std={ref.std():.4f}')

dims2, ttype2, off2, data_off2 = find_tensor(SEQ, TENSOR)
with open(SEQ, 'rb') as f:
    f.seek(data_off2 + off2)
    seq_all = np.frombuffer(f.read((n_elem // 256) * 65), dtype=np.uint8)

base = [-1.5, -0.5, 0.5, 1.5]
perms = list(itertools.permutations(base))
results = []

def f16_from_bytes(blocks, o):
    return np.frombuffer(blocks[:, o:o+2].tobytes(), dtype='<f2').astype(np.float32)

# 结构 1: 65B/256, scale u8 在 pos1, 码 {0,2..64}
for div in (255.0, 256.0, 127.0, 1.0):
    for order in ('linear', 's16'):
        for bits in ('lsb', 'msb'):
            for perm in perms:
                code_off = 0
                # 手工构造: 码字节 = [b0, b2..b64]
                blocks = seq_all.reshape(-1, 65)
                code = np.concatenate([blocks[:, 0:1], blocks[:, 2:65]], axis=1)
                sc = blocks[:, 1].astype(np.float32) / div
                cand = None
                # 复用 decode_seq: 特殊处理, 直接在这里算
                lv = np.array(perm, dtype=np.float32)
                if bits == 'lsb':
                    lanes = [(code & 3), ((code >> 2) & 3), ((code >> 4) & 3), ((code >> 6) & 3)]
                else:
                    lanes = [((code >> 6) & 3), ((code >> 4) & 3), ((code >> 2) & 3), (code & 3)]
                vals = [lv[l] for l in lanes]
                imap = seq_imap_256(order)
                out = np.empty(4096 * 256, dtype=np.float32).reshape(4096, 256)
                for p in range(4):
                    out[:, imap[:, p]] = vals[p] * sc[:, None]
                cand = out.reshape(-1)[:n_elem]
                r = pearson(ref, cand); sgn = sign_agree(ref, cand)
                results.append((sgn, r, '65B-scale@1', div, order, bits, perm))

# 结构 2: 130B/512, f16 scale 在块首 [0:2], 码 [2:130]
for order in ('linear', 's16'):
    for bits in ('lsb', 'msb'):
        for perm in perms:
            blk = seq_all.reshape(-1, 130)
            sc = f16_from_bytes(blk, 0)
            code = blk[:, 2:130]
            lv = np.array(perm, dtype=np.float32)
            if bits == 'lsb':
                lanes = [(code & 3), ((code >> 2) & 3), ((code >> 4) & 3), ((code >> 6) & 3)]
            else:
                lanes = [((code >> 6) & 3), ((code >> 4) & 3), ((code >> 2) & 3), (code & 3)]
            vals = [lv[l] for l in lanes]
            imap = seq_imap_512(order)
            out = np.empty(2048 * 512, dtype=np.float32).reshape(2048, 512)
            for p in range(4):
                out[:, imap[:, p]] = vals[p] * sc[:, None]
            cand = out.reshape(-1)[:n_elem]
            r = pearson(ref, cand); sgn = sign_agree(ref, cand)
            results.append((sgn, r, '130B-f16@0', 1.0, order, bits, perm))

# 结构 3: 130B/512, f16 scale 在块尾 [128:130], 码 [0:128]
for order in ('linear', 's16'):
    for bits in ('lsb', 'msb'):
        for perm in perms:
            blk = seq_all.reshape(-1, 130)
            sc = f16_from_bytes(blk, 128)
            code = blk[:, 0:128]
            lv = np.array(perm, dtype=np.float32)
            if bits == 'lsb':
                lanes = [(code & 3), ((code >> 2) & 3), ((code >> 4) & 3), ((code >> 6) & 3)]
            else:
                lanes = [((code >> 6) & 3), ((code >> 4) & 3), ((code >> 2) & 3), (code & 3)]
            vals = [lv[l] for l in lanes]
            imap = seq_imap_512(order)
            out = np.empty(2048 * 512, dtype=np.float32).reshape(2048, 512)
            for p in range(4):
                out[:, imap[:, p]] = vals[p] * sc[:, None]
            cand = out.reshape(-1)[:n_elem]
            r = pearson(ref, cand); sgn = sign_agree(ref, cand)
            results.append((sgn, r, '130B-f16@128', 1.0, order, bits, perm))

results.sort(reverse=True)
print('\n===== top 25 =====')
for sgn, r, struct, div, order, bits, perm in results[:25]:
    print('sgn=%.3f corr=%+.3f %-13s div=%-6g order=%-6s bits=%-3s levels=%s' % (sgn, r, struct, div, order, bits, perm))
