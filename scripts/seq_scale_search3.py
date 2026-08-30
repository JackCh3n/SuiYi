# -*- coding: utf-8 -*-
"""s16 码序下, 搜索最优 scale 位置/类型 (MT1.5 STQ 参考)"""
import struct
import numpy as np

def read_str(f):
    ln = struct.unpack('<Q', f.read(8))[0]
    return f.read(ln).decode('utf-8', 'replace')
def skip(f, t):
    if t in (0,1,7): f.read(1)
    elif t in (2,3): f.read(2)
    elif t in (4,5,6): f.read(4)
    elif t==8: read_str(f)
    elif t==9:
        et=struct.unpack('<I',f.read(4))[0]; n=struct.unpack('<Q',f.read(8))[0]
        for _ in range(n): skip(f,et)
    elif t in (10,11,12): f.read(8)
def find_tensor(path, tname):
    f = open(path, 'rb'); f.read(4); f.read(4); n_t = struct.unpack('<Q', f.read(8))[0]; n_kv = struct.unpack('<Q', f.read(8))[0]
    for _ in range(n_kv):
        read_str(f); skip(f, struct.unpack('<I', f.read(4))[0])
    found = None
    for i in range(n_t):
        name = read_str(f); nd = struct.unpack('<I', f.read(4))[0]
        dims = [struct.unpack('<q', f.read(8))[0] for _ in range(nd)]
        ttype = struct.unpack('<I', f.read(4))[0]; off = struct.unpack('<Q', f.read(8))[0]
        if name == tname:
            found = (dims, ttype, off)
    data_off = (f.tell() + 31) // 32 * 32
    f.close()
    return (found[0], found[1], found[2], data_off) if found else None

MT15 = r'models/Hy-MT1.5-1.8B-1.25bit.gguf'
SEQ = r'models/Hy-MT2-1.8B-2bit-v2.gguf'
TENSOR = 'blk.0.attn_k.weight'
d1, t1, o1, d1o = find_tensor(MT15, TENSOR)
n = d1[0]*d1[1]
with open(MT15,'rb') as f:
    f.seek(d1o+o1); mt_raw = np.frombuffer(f.read((n//256)*42), dtype=np.uint8)
d2, t2, o2, d2o = find_tensor(SEQ, TENSOR)
with open(SEQ,'rb') as f:
    f.seek(d2o+o2); seq_all = np.frombuffer(f.read((n//256)*65), dtype=np.uint8)

CODEBOOK = [0xA9,0x89,0x29,0x09,0xA6,0x86,0x26,0x06,0x9A,0x92,0x1A,0x12,0x6A,0x62,0x4A,0x42,
            0x01,0x21,0x81,0xA1,0x04,0x24,0x84,0xA4,0x10,0x18,0x90,0x98,0x40,0x48,0x60,0x68]
def stq_block(blk):
    d = np.frombuffer(blk[:, 40:42].tobytes(), dtype='<f2').astype(np.float32)
    qs = blk[:, 0:32]; sgn = blk[:, 32:40]
    nb = len(blk); out = np.empty((nb, 256), dtype=np.float32)
    cb = np.array(CODEBOOK, dtype=np.int32)
    for g in range(64):
        code = (qs[:, g//2] >> (4*(g&1))) & 0xF
        s = (sgn[:, g//8] >> (g%8)) & 1
        qp = cb[(s<<4) | code]
        chunk, gloc = g//16, g%16
        for p in range(4):
            q = (qp >> (2*p)) & 3
            out[:, chunk*64 + gloc + p*16] = (q-1) * d
    return out
ref_stq = stq_block(mt_raw.reshape(-1,42)).reshape(-1)[:n]

nblk = n // 512
b130 = seq_all.reshape(nblk, 130)
code = b130[:, 2:130]
lv = np.array([-1.5,-0.5,0.5,1.5], dtype=np.float32)
lanes = [(code&3), ((code>>2)&3), ((code>>4)&3), ((code>>6)&3)]
vals = [lv[l] for l in lanes]
D = np.empty((nblk, 512), dtype=np.float32)
for g in range(128):
    chunk, gloc = g//16, g%16
    for p in range(4):
        D[:, chunk*64 + gloc + p*16] = vals[p][:, g]
ref512 = ref_stq.reshape(nblk, 512)

def pearson_all(cand):
    a=cand.reshape(-1)-cand.reshape(-1).mean(); b=ref_stq-ref_stq.mean()
    return float((a*b).sum()/np.sqrt((a*a).sum()*(b*b).sum()))

results = []
# f16 scale 位置 (每 2 字节)
for pos in range(0, 129, 2):
    sc = np.frombuffer(b130[:, pos:pos+2].tobytes(), dtype='<f2').astype(np.float32)
    cand = D * sc[:,None]
    r = pearson_all(cand)
    results.append((abs(r), r, 'f16@%d' % pos))
# u8 scale 位置
for pos in range(130):
    sc = b130[:, pos].astype(np.float32) / 255.0
    cand = D * sc[:,None]
    r = pearson_all(cand)
    results.append((abs(r), r, 'u8/255@%d' % pos))

results.sort(reverse=True)
print('===== 最优 scale 位置 top 20 =====')
for ar, r, tag in results[:20]:
    print('  |r|=%.4f r=%+.4f  %s' % (ar, r, tag))
