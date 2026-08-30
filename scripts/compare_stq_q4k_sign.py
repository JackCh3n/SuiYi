# -*- coding: utf-8 -*-
"""对比 -v2 STQ 反量化（PR 码本 + f16 scale）与 Q4_K 真实权重的符号一致性，判断码本是否匹配"""
import struct

STQ1_0_CODEBOOK = [0xA9,0x89,0x29,0x09,0xA6,0x86,0x26,0x06,0x9A,0x92,0x1A,0x12,0x6A,0x62,0x4A,0x42,
                   0x01,0x21,0x81,0xA1,0x04,0x24,0x84,0xA4,0x10,0x18,0x90,0x98,0x40,0x48,0x60,0x68]

def get_scale_min_k4(j, q):
    if j < 4:
        d = q[j] & 63; m = q[j+4] & 63
    else:
        d = (q[j+4] & 0xF) | ((q[j-4] >> 6) << 4)
        m = (q[j+4] >> 4) | ((q[j] >> 6) << 4)
    return d, m

def dequant_q4k_block(blk):
    d = struct.unpack('<e', blk[0:2])[0]
    dmin = struct.unpack('<e', blk[2:4])[0]
    scales = blk[4:16]
    qs = blk[16:144]
    out = []
    is_ = 0
    for j in range(0, 256, 64):
        sc, m = get_scale_min_k4(is_+0, scales); d1 = d*sc; m1 = dmin*m
        sc, m = get_scale_min_k4(is_+1, scales); d2 = d*sc; m2 = dmin*m
        for l in range(32): out.append(d1*(qs[l]&0xF) - m1)
        for l in range(32): out.append(d2*(qs[l]>>4) - m2)
        qs = qs[32:]; is_ += 2
    return out

def dequant_stq_block(blk):
    d = struct.unpack('<e', blk[40:42])[0]
    qs, sgn = blk[0:32], blk[32:40]
    out = [0.0]*256
    for g in range(64):
        code = (qs[g//2] >> (4*(g&1))) & 0x0F
        s = (sgn[g//8] >> (g%8)) & 0x01
        qpack = STQ1_0_CODEBOOK[(s<<4)|code]
        chunk, gloc = g//16, g%16
        for p in range(4):
            q = (qpack >> (2*p)) & 0x3
            out[chunk*64 + gloc + p*16] = (q-1)*d
    return out

def load_tensor(path, off, blk_size, nblk):
    with open(path,'rb') as f:
        f.seek(off)
        return f.read(blk_size*nblk)

# 两个文件的 attn_k 都在 off=202982432
q4k = load_tensor(r'models/Hy-MT2-1.8B-Q4_K_M.gguf', 202982432, 144, 8)
stq = load_tensor(r'models/Hy-MT2-1.8B-1.25bit-v2.gguf', 202982432, 42, 8)

agree = 0; total = 0; agree_nz = 0; total_nz = 0
for i in range(8):
    q4 = dequant_q4k_block(q4k[i*144:(i+1)*144])
    st = dequant_stq_block(stq[i*42:(i+1)*42])
    for a, b in zip(q4, st):
        sa = 1 if a > 0 else (-1 if a < 0 else 0)
        sb = 1 if b > 0 else (-1 if b < 0 else 0)
        if sa != 0 or sb != 0:
            total_nz += 1
            if sa == sb: agree_nz += 1
        total += 1
        if sa == sb: agree += 1
print("全元素符号一致率: %.1f%% (%d/%d)" % (100.0*agree/total, agree, total))
print("非零元素符号一致率: %.1f%% (%d/%d)" % (100.0*agree_nz/total_nz, agree_nz, total_nz))
print("(若 ~100% 码本匹配仅 scale 问题；若 ~50% 码本不同)")
