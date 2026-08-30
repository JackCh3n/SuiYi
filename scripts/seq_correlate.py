# -*- coding: utf-8 -*-
"""以 Hy-MT1.5 1.25bit(STQ1_0) 反量化为基准，测 2bit SEQ 各种解码假设的相关系数"""
import struct
import itertools

CODEBOOK = [0xA9,0x89,0x29,0x09,0xA6,0x86,0x26,0x06,0x9A,0x92,0x1A,0x12,0x6A,0x62,0x4A,0x42,
            0x01,0x21,0x81,0xA1,0x04,0x24,0x84,0xA4,0x10,0x18,0x90,0x98,0x40,0x48,0x60,0x68]

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
    f=open(path,'rb'); f.read(4); f.read(4); n_t=struct.unpack('<Q',f.read(8))[0]; n_kv=struct.unpack('<Q',f.read(8))[0]
    for _ in range(n_kv):
        read_str(f); skip(f,struct.unpack('<I',f.read(4))[0])
    for i in range(n_t):
        name=read_str(f); nd=struct.unpack('<I',f.read(4))[0]
        dims=[struct.unpack('<q',f.read(8))[0] for _ in range(nd)]
        ttype=struct.unpack('<I',f.read(4))[0]; off=struct.unpack('<Q',f.read(8))[0]
        if name==tname:
            f.close(); return dims, ttype, off
    f.close(); return None

def dequant_stq_block(blk):
    d = struct.unpack('<e', blk[40:42])[0]
    qs, sgn = blk[0:32], blk[32:40]
    out = [0.0]*256
    for g in range(64):
        code = (qs[g//2] >> (4*(g&1))) & 0x0F
        s = (sgn[g//8] >> (g%8)) & 0x01
        qpack = CODEBOOK[(s<<4)|code]
        chunk, gloc = g//16, g%16
        for p in range(4):
            q = (qpack >> (2*p)) & 0x3
            out[chunk*64 + gloc + p*16] = (q-1)*d
    return out

def dequant_seq_block(blk, levels, order, div):
    scale = blk[64] / div
    out = [0.0]*256
    if order == 'linear':
        for i in range(64):
            b = blk[i]
            out[i*4+0] = levels[b&3]*scale
            out[i*4+1] = levels[(b>>2)&3]*scale
            out[i*4+2] = levels[(b>>4)&3]*scale
            out[i*4+3] = levels[(b>>6)&3]*scale
    else:  # s16
        for g in range(64):
            b = blk[g]
            chunk, gloc = g//16, g%16
            base = chunk*64 + gloc
            out[base] = levels[b&3]*scale
            out[base+16] = levels[(b>>2)&3]*scale
            out[base+32] = levels[(b>>4)&3]*scale
            out[base+48] = levels[(b>>6)&3]*scale
    return out

def pearson(a, b):
    n = len(a)
    ma = sum(a)/n; mb = sum(b)/n
    num = sum((x-ma)*(y-mb) for x,y in zip(a,b))
    da = sum((x-ma)**2 for x in a) ** 0.5
    db = sum((y-mb)**2 for y in b) ** 0.5
    if da==0 or db==0: return 0
    return num/(da*db)

# 基准：Hy-MT1.5 1.25bit
d1, t1, o1 = find_tensor(r'models/Hy-MT1.5-1.8B-1.25bit.gguf', 'blk.0.attn_k.weight')
print('1.25bit attn_k: dims=%s type=%d off=%d' % (d1, t1, o1))
f=open(r'models/Hy-MT1.5-1.8B-1.25bit.gguf','rb'); f.seek(o1); ref1=f.read(42*64); f.close()
ref = []
for i in range(64):
    ref.extend(dequant_stq_block(ref1[i*42:(i+1)*42]))

# 2bit
d2, t2, o2 = find_tensor(r'models/Hy-MT2-1.8B-2bit-v2.gguf', 'blk.0.attn_k.weight')
print('2bit attn_k: dims=%s type=%d off=%d' % (d2, t2, o2))
f=open(r'models/Hy-MT2-1.8B-2bit-v2.gguf','rb'); f.seek(o2); data2=f.read(65*64); f.close()

print('\n相关度 (基准=1.25bit 前64块):')
for order in ('linear','s16'):
    for div in (255, 170, 128, 64):
        for levels in ([-1.5,-0.5,0.5,1.5], [1.5,0.5,-0.5,-1.5], [-0.5,-1.5,1.5,0.5], [0.5,1.5,-1.5,-0.5]):
            cand=[]
            for i in range(64):
                cand.extend(dequant_seq_block(data2[i*65:(i+1)*65], levels, order, div))
            r = pearson(ref, cand)
            if abs(r) > 0.3:
                print('  order=%-6s div=%-3s levels=%s => r=%.3f' % (order, div, levels, r))
