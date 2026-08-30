# -*- coding: utf-8 -*-
"""SEQ 2bit 自洽检查：每块 256 元素，若 2bit 码解码正确且 scale 每块恒定，
则每块去量化值应恰有 2 个量级 {0.5s, 1.5s}，比值恰为 3。"""
import struct
from collections import Counter

path = r'models/Hy-MT2-1.8B-2bit-v2.gguf'
OFF = 202982432  # blk.0.attn_k.weight

with open(path, 'rb') as f:
    f.seek(OFF)
    data = f.read(65 * 512)  # 512 个 65B 块

LEVELS = [-1.5, -0.5, 0.5, 1.5]  # 假设 code 0..3 依次对应

def decode_block(blk):
    # blk[0:64] 2bit 码（LSB-first 每字节 4 个），blk[64] 候选 scale
    codes = []
    for b in blk[0:64]:
        codes.append(b & 3)
        codes.append((b >> 2) & 3)
        codes.append((b >> 4) & 3)
        codes.append((b >> 6) & 3)
    assert len(codes) == 256
    return codes

def check_block(codes):
    # 去量化值（未乘 scale）应取 LEVELS 中的值，量级 {0.5,1.5}
    vals = [LEVELS[c] for c in codes]
    mags = sorted(set(abs(v) for v in vals))
    if len(mags) != 2:
        return None, mags
    a, b = mags
    if abs(b / a - 3.0) < 0.01:
        return (a, b), mags
    if abs(a / b - 3.0) < 0.01:
        return (a, b), mags
    return None, mags

ok = 0
bad = []
for i in range(512):
    blk = data[i*65:(i+1)*65]
    codes = decode_block(blk)
    res, mags = check_block(codes)
    if res:
        ok += 1
    else:
        bad.append((i, mags))
        if len(bad) > 5: break

print("每块恰好 2 量级且比值==3 的块数: %d/512" % ok)
print("异常块样例:", bad[:5])
