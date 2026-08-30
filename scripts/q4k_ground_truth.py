# -*- coding: utf-8 -*-
"""正确反量化 Q4_K_M 的 blk.0.attn_k.weight，得到真实权重分布"""
import struct

def get_scale_min_k4(j, q):
    if j < 4:
        d = q[j] & 63
        m = q[j + 4] & 63
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
        sc, m = get_scale_min_k4(is_ + 0, scales)
        d1 = d * sc; m1 = dmin * m
        sc, m = get_scale_min_k4(is_ + 1, scales)
        d2 = d * sc; m2 = dmin * m
        for l in range(32):
            out.append(d1 * (qs[l] & 0xF) - m1)
        for l in range(32):
            out.append(d2 * (qs[l] >> 4) - m2)
        qs = qs[32:]
        is_ += 2
    return out

with open(r'models/Hy-MT2-1.8B-Q4_K_M.gguf', 'rb') as f:
    f.seek(202982432)
    raw = f.read(144 * 8)

for i in range(8):
    vals = dequant_q4k_block(raw[i*144:(i+1)*144])
    mx = max(abs(v) for v in vals)
    mean = sum(abs(v) for v in vals)/len(vals)
    big = sum(1 for v in vals if abs(v) > 1.0)
    print('Q4_K blk%d: max|w|=%.4g mean|w|=%.4g |w|>1 个数=%d/256' % (i, mx, mean, big))
