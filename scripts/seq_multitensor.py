# -*- coding: utf-8 -*-
"""多张量解码相关性: 检查是否所有 SEQ 张量都解码良好"""
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

def dequant_stq_bytes(raw, n):
    blk = raw.reshape(-1, 42)
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
    return out.reshape(-1)[:n]

def dequant_seq_s16(raw, n):
    nblk = n // 512
    b = raw.reshape(nblk, 130)
    sc = np.frombuffer(b[:, 0:2].tobytes(), dtype='<f2').astype(np.float32)
    code = b[:, 2:130]
    lv = np.array([-1.5,-0.5,0.5,1.5], dtype=np.float32)
    lanes = [(code&3), ((code>>2)&3), ((code>>4)&3), ((code>>6)&3)]
    vals = [lv[l] for l in lanes]
    D = np.empty((nblk, 512), dtype=np.float32)
    for g in range(128):
        chunk, gloc = g//16, g%16
        for p in range(4):
            D[:, chunk*64 + gloc + p*16] = vals[p][:, g]
    return (D * sc[:,None]).reshape(-1)[:n]

def pearson(a,b):
    a=a-a.mean(); b=b-b.mean()
    return float((a*b).sum()/np.sqrt((a*a).sum()*(b*b).sum()))

CODEBOOK = [0xA9,0x89,0x29,0x09,0xA6,0x86,0x26,0x06,0x9A,0x92,0x1A,0x12,0x6A,0x62,0x4A,0x42,
            0x01,0x21,0x81,0xA1,0x04,0x24,0x84,0xA4,0x10,0x18,0x90,0x98,0x40,0x48,0x60,0x68]

tensors = ['blk.0.attn_q.weight', 'blk.0.attn_k.weight', 'blk.0.attn_v.weight',
           'blk.0.attn_output.weight', 'blk.0.ffn_gate.weight', 'blk.0.ffn_up.weight',
           'blk.0.ffn_down.weight', 'blk.3.attn_k.weight', 'blk.15.attn_q.weight',
           'blk.31.ffn_down.weight']
for tname in tensors:
    r1 = find_tensor(r'models/Hy-MT1.5-1.8B-1.25bit.gguf', tname)
    r2 = find_tensor(r'models/Hy-MT2-1.8B-2bit-v2.gguf', tname)
    if not r1 or not r2:
        print('%-28s 缺失' % tname); continue
    n = r1[0][0]*r1[0][1]
    with open(r'models/Hy-MT1.5-1.8B-1.25bit.gguf','rb') as f:
        f.seek(r1[3]+r1[2]); mt = np.frombuffer(f.read((n//256)*42), dtype=np.uint8)
    with open(r'models/Hy-MT2-1.8B-2bit-v2.gguf','rb') as f:
        f.seek(r2[3]+r2[2]); sq = np.frombuffer(f.read((n//256)*65), dtype=np.uint8)
    ref = dequant_stq_bytes(mt, n)
    cand = dequant_seq_s16(sq, n)
    r = pearson(ref, cand)
    sgn = (np.sign(ref)==np.sign(cand)).mean()
    print('%-28s corr=%+.4f sgn=%.3f' % (tname, r, sgn))
