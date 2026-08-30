# -*- coding: utf-8 -*-
"""对比 2bit(SEQ s16/lsb/up) 与 1.25bit(STQ) attn_k 的行范数分布"""
import struct, statistics

CODEBOOK=[0xA9,0x89,0x29,0x09,0xA6,0x86,0x26,0x06,0x9A,0x92,0x1A,0x12,0x6A,0x62,0x4A,0x42,0x01,0x21,0x81,0xA1,0x04,0x24,0x84,0xA4,0x10,0x18,0x90,0x98,0x40,0x48,0x60,0x68]
LV=[-1.5,-0.5,0.5,1.5]

def find_tensor(path, tname):
    def read_str(f):
        ln=struct.unpack('<Q',f.read(8))[0]; return f.read(ln).decode('utf-8','replace')
    def skip(f,t):
        if t in (0,1,7): f.read(1)
        elif t in (2,3): f.read(2)
        elif t in (4,5,6): f.read(4)
        elif t==8: read_str(f)
        elif t==9:
            et=struct.unpack('<I',f.read(4))[0]; n=struct.unpack('<Q',f.read(8))[0]
            for _ in range(n): skip(f,et)
        elif t in (10,11,12): f.read(8)
    f=open(path,'rb'); f.read(4); f.read(4); n_t=struct.unpack('<Q',f.read(8))[0]; n_kv=struct.unpack('<Q',f.read(8))[0]
    for _ in range(n_kv):
        read_str(f); skip(f,struct.unpack('<I',f.read(4))[0])
    for i in range(n_t):
        name=read_str(f); nd=struct.unpack('<I',f.read(4))[0]
        dims=[struct.unpack('<q',f.read(8))[0] for _ in range(nd)]
        ttype=struct.unpack('<I',f.read(4))[0]; off=struct.unpack('<Q',f.read(8))[0]
        if name==tname:
            f.close(); return off
    f.close(); return None

def stq_row(blk42s):
    out=[]
    for i in range(2):
        blk=blk42s[i*42:(i+1)*42]
        d=struct.unpack('<e',blk[40:42])[0]
        qs,sgn=blk[0:32],blk[32:40]
        row=[0.0]*256
        for g in range(64):
            code=(qs[g//2]>>(4*(g&1)))&0x0F
            s=(sgn[g//8]>>(g%8))&0x01
            qp=CODEBOOK[(s<<4)|code]
            chunk,gloc=g//16,g%16
            for p in range(4):
                q=(qp>>(2*p))&0x3
                row[chunk*64+gloc+p*16]=(q-1)*d
        out.extend(row)
    return out

def seq_row(blk130):
    scale=struct.unpack('<e',blk130[0:2])[0]
    out=[0.0]*512
    for g in range(128):
        b=blk130[2+g]
        chunk,gloc=g//16,g%16
        b0=chunk*64+gloc
        out[b0]=LV[b&3]*scale
        out[b0+16]=LV[(b>>2)&3]*scale
        out[b0+32]=LV[(b>>4)&3]*scale
        out[b0+48]=LV[(b>>6)&3]*scale
    return out

def row_norms(values, rows, cols):
    return [sum(v*v for v in values[r*cols:(r+1)*cols])**0.5 for r in range(rows)]

# 1.25bit Hy-MT1.5
o1=find_tensor(r'models/Hy-MT1.5-1.8B-1.25bit.gguf','blk.0.attn_k.weight')
f=open(r'models/Hy-MT1.5-1.8B-1.25bit.gguf','rb'); f.seek(o1); d1=f.read(42*2048*2); f.close()
norms1=[]
for r in range(512):
    norms1.append(sum(v*v for v in stq_row(d1[r*84:(r+1)*84]))**0.5)

# 2bit Hy-MT1.5(文件名叫Hy-MT2)
o2=find_tensor(r'models/Hy-MT2-1.8B-2bit-v2.gguf','blk.0.attn_k.weight')
f=open(r'models/Hy-MT2-1.8B-2bit-v2.gguf','rb'); f.seek(o2); d2=f.read(130*512); f.close()
norms2=[]
for r in range(512):
    norms2.append(sum(v*v for v in seq_row(d2[r*130:(r+1)*130]))**0.5)

def stats(x):
    s=sorted(x)
    return 'min=%.3g p50=%.3g p90=%.3g max=%.3g' % (s[0], s[len(s)//2], s[int(len(s)*0.9)], s[-1])

print('1.25bit Hy-MT1.5 attn_k 行范数 (512行):', stats(norms1))
print('2bit SEQ(s16/lsb/up) attn_k 行范数 (512行):', stats(norms2))
