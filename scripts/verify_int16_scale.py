# -*- coding: utf-8 -*-
"""严格验证：-v2 格式 scale 是 int16(÷32768)，且数据字节与 PR #22836 完全一致，只需转 f16"""
import struct, sys
from collections import Counter

STQ1_0_CODEBOOK = [0xA9,0x89,0x29,0x09,0xA6,0x86,0x26,0x06,0x9A,0x92,0x1A,0x12,0x6A,0x62,0x4A,0x42,
                   0x01,0x21,0x81,0xA1,0x04,0x24,0x84,0xA4,0x10,0x18,0x90,0x98,0x40,0x48,0x60,0x68]

def read_str(f):
    ln = struct.unpack("<Q", f.read(8))[0]
    return f.read(ln).decode("utf-8", errors="replace")

def skip_value(f, vtype):
    if vtype in (0,1,7): f.read(1)
    elif vtype in (2,3): f.read(2)
    elif vtype in (4,5,6): f.read(4)
    elif vtype == 8: read_str(f)
    elif vtype == 9:
        atype=struct.unpack("<I",f.read(4))[0]; cnt=struct.unpack("<Q",f.read(8))[0]
        for _ in range(cnt): skip_value(f, atype)
    elif vtype in (10,11,12): f.read(8)

path=sys.argv[1]
with open(path,"rb") as f:
    assert f.read(4)==b"GGUF"
    version=struct.unpack("<I",f.read(4))[0]
    n_t=struct.unpack("<Q",f.read(8))[0]
    n_kv=struct.unpack("<Q",f.read(8))[0]
    for _ in range(n_kv):
        read_str(f); skip_value(f, struct.unpack("<I",f.read(4))[0])
    for i in range(n_t):
        name=read_str(f)
        nd=struct.unpack("<I",f.read(4))[0]
        dims=[struct.unpack("<q",f.read(8))[0] for _ in range(nd)]
        ttype=struct.unpack("<I",f.read(4))[0]
        off=struct.unpack("<Q",f.read(8))[0]
        if ttype==40:
            break
    print("张量:",name,dims,"off=",off)
    f.seek(off)
    data=f.read(42*1024)  # 1024 块

# 检查 int16/32768 是否为合理 scale
scales=[]
for i in range(1024):
    blk=data[i*42:(i+1)*42]
    s16=struct.unpack("<h", blk[40:42])[0]
    scales.append(s16/32768.0)
plaus=[s for s in scales if 0.0005<=abs(s)<=1.0]
print("\nint16/32768 scale 合理占比: %d/%d" % (len(plaus), len(scales)))
print("scale 分布: min=%.5g max=%.5g" % (min(abs(s) for s in scales), max(abs(s) for s in scales)))
print("负数 scale 个数:", sum(1 for s in scales if s<0))
print("前20 scale:", [round(s,5) for s in scales[:20]])

# 用 int16 scale 反量化并检查值分布（应全部 |v|<=1, 75% 非零）
vals=[]
for i in range(256):
    blk=data[i*42:(i+1)*42]
    d=struct.unpack("<h", blk[40:42])[0]/32768.0
    qs,sgn=blk[0:32],blk[32:40]
    for g in range(64):
        code=(qs[g//2]>>(4*(g&1)))&0x0F
        s=(sgn[g//8]>>(g%8))&0x01
        qpack=STQ1_0_CODEBOOK[(s<<4)|code]
        for p in range(4):
            q=(qpack>>(2*p))&0x3
            v=(q-1)*d
            vals.append(v)
nz=[v for v in vals if v!=0]
print("\n反量化 256 块: 非零占比 %.1f%% (期望75%%)" % (100.0*len(nz)/len(vals)))
print("|v| 范围: min=%.5g max=%.5g (期望 <=1)" % (min(abs(v) for v in nz), max(abs(v) for v in nz)))
print("值分布 top8:", Counter(round(v,4) for v in vals).most_common(8))

# 关键验证：scale 是否精确等于块内最大 |v|（即 absmax 一致性）
print("\n一致性检查：scale 是否等于块内 |v|max？")
ok=0
for i in range(256):
    blk=data[i*42:(i+1)*42]
    d=struct.unpack("<h", blk[40:42])[0]/32768.0
    if d==0: continue
    qs,sgn=blk[0:32],blk[32:40]
    mx=0
    for g in range(64):
        code=(qs[g//2]>>(4*(g&1)))&0x0F
        s=(sgn[g//8]>>(g%8))&0x01
        qpack=STQ1_0_CODEBOOK[(s<<4)|code]
        for p in range(4):
            q=(qpack>>(2*p))&0x3
            mx=max(mx, abs((q-1)*d))
    if abs(mx-abs(d))<1e-6*max(1,abs(d)):
        ok+=1
print("|v|max == |scale| 的块: %d/%d" % (ok, 256))
