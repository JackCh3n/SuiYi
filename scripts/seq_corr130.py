# -*- coding: utf-8 -*-
"""130B/512 结构下，测 2bit 解码与 1.25bit Hy-MT1.5 参考的相关性"""
import struct, itertools

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
        qp = CODEBOOK[(s<<4)|code]
        chunk, gloc = g//16, g%16
        for p in range(4):
            q = (qp >> (2*p)) & 0x3
            out[chunk*64 + gloc + p*16] = (q-1)*d
    return out

def dequant_seq(blk130, levels, order, bits):
    scale = struct.unpack('<e', blk130[0:2])[0]
    out = [0.0]*512
    def lane(b, i):
        if bits=='lsb':
            return (b>>(2*i))&3
        else:
            return (b>>(6-2*i))&3
    if order=='linear':
        for i in range(128):
            b = blk130[2+i]
            out[i*4+0] = levels[lane(b,0)]*scale
            out[i*4+1] = levels[lane(b,1)]*scale
            out[i*4+2] = levels[lane(b,2)]*scale
            out[i*4+3] = levels[lane(b,3)]*scale
    else:  # s16
        for g in range(128):
            b = blk130[2+g]
            chunk, gloc = g//16, g%16
            b0 = chunk*64 + gloc
            out[b0] = levels[lane(b,0)]*scale
            out[b0+16] = levels[lane(b,1)]*scale
            out[b0+32] = levels[lane(b,2)]*scale
            out[b0+48] = levels[lane(b,3)]*scale
    return out

def pearson(a, b):
    n = len(a)
    ma = sum(a)/n; mb = sum(b)/n
    num = sum((x-ma)*(y-mb) for x,y in zip(a,b))
    da = sum((x-ma)**2 for x in a) ** 0.5
    db = sum((y-mb)**2 for y in b) ** 0.5
    return num/(da*db) if da and db else 0

# 基准
d1,t1,o1 = find_tensor(r'models/Hy-MT1.5-1.8B-1.25bit.gguf','blk.0.attn_k.weight')
f=open(r'models/Hy-MT1.5-1.8B-1.25bit.gguf','rb'); f.seek(o1); ref1=f.read(42*128); f.close()
ref=[]
for i in range(128): ref.extend(dequant_stq_block(ref1[i*42:(i+1)*42]))

# 2bit
d2,t2,o2 = find_tensor(r'models/Hy-MT2-1.8B-2bit-v2.gguf','blk.0.attn_k.weight')
f=open(r'models/Hy-MT2-1.8B-2bit-v2.gguf','rb'); f.seek(o2); data2=f.read(130*64); f.close()

print('相关度 (基准=1.25bit Hy-MT1.5 前128块):')
results=[]
base=[-1.5,-0.5,0.5,1.5]
for order in ('linear','s16'):
    for bits in ('lsb','msb'):
        for perm in itertools.permutations(base):
            cand=[]
            for i in range(64):
                cand.extend(dequant_seq(data2[i*130:(i+1)*130], list(perm), order, bits))
            r = pearson(ref, cand)
            results.append((abs(r), order, bits, perm))
results.sort(reverse=True)
for r, order, bits, perm in results[:8]:
    print('  |r|=%.3f order=%-6s bits=%-3s levels=%s' % (r, order, bits, perm))
