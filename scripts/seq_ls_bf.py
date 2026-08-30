# -*- coding: utf-8 -*-
"""LS最优scale解耦: 对块大小 256/512/1024/2048, 搜索码序+位序+level, 以相关最大化"""
import struct, itertools, sys
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

Q4K = r'models/Hy-MT2-1.8B-Q4_K_M.gguf'
SEQ = r'models/Hy-MT2-1.8B-2bit-v2.gguf'
TENSOR = 'blk.0.attn_k.weight'
dims, ttype, off, data_off = find_tensor(Q4K, TENSOR)
n = dims[0]*dims[1]
with open(Q4K,'rb') as f:
    f.seek(data_off+off); q4k = np.frombuffer(f.read((n//256)*144), dtype=np.uint8)
with open(SEQ,'rb') as f:
    f.seek(data_off+off); seq_all = np.frombuffer(f.read((n//256)*65), dtype=np.uint8)

def dequant_q4k(data, n):
    blocks = data.reshape(-1, 144); nb = len(blocks)
    d = np.frombuffer(blocks[:,0:2].tobytes(), dtype='<f2').astype(np.float32)
    dmin = np.frombuffer(blocks[:,2:4].tobytes(), dtype='<f2').astype(np.float32)
    qs = blocks[:,16:144]; sc = blocks[:,4:16]
    out = np.empty(nb*256, dtype=np.float32)
    for j in range(8):
        if j < 4:
            dl = sc[:,j]&63; ml = sc[:,j+4]&63
        else:
            dl = (sc[:,j+4]&0xF)|((sc[:,j-4]>>6)<<4); ml = (sc[:,j+4]>>4)|((sc[:,j-0]>>6)<<4)
        d_scaled = d*dl.astype(np.float32); m_scaled = dmin*ml.astype(np.float32)
        qq = qs[:,j*16:(j+1)*16]
        lo=(qq&0xF).astype(np.float32); hi=((qq>>4)&0xF).astype(np.float32)
        col = d_scaled[:,None]*np.concatenate([lo,hi],axis=1).reshape(nb,32) - m_scaled[:,None]
        out.reshape(nb,256)[:,j*32:(j+1)*32] = col
    return out[:n]

ref = dequant_q4k(q4k, n)

# 块大小 -> 每块元素数, 码字节数, 每块字节数 (全部字节当码)
CONFIGS = {
    '256e/64c': (256, 64),   # 256 元素, 64 码字节 (=65B, 1 额外字节)
    '512e/128c': (512, 128), # 512 元素, 128 码字节 (=130B, 2 额外)
    '1024e/256c': (1024, 256),
    '2048e/512c': (2048, 512),
}

def imap_linear(nc):
    return np.array([[i*4+p for p in range(4)] for i in range(nc)], dtype=np.int64)
def imap_s16(nc, bs):
    # 沿用 STQ 风格: chunk=bs//16 组, 每组跨 16 距离
    # group g -> chunk*? + gloc + p*16; 令每 16 个连续组为一组, 组间步进 16*4=64? 
    # 通用: 每 16 组覆盖 64 个连续位置(lane p 步进16), 之后下一 16 组从 +64 开始
    per = 16
    nchunk = nc // per
    return np.array([[(g//per)*(per*4) + (g%per) + p*per for p in range(4)] for g in range(nc)], dtype=np.int64)

def score(order, bits, perm, bs, nc):
    """返回 (corr, sign)"""
    nblk = n // bs
    blocks = seq_all[:nblk*nc].reshape(nblk, nc)
    lv = np.array(perm, dtype=np.float32)
    if bits == 'lsb':
        lanes = [(blocks&3), ((blocks>>2)&3), ((blocks>>4)&3), ((blocks>>6)&3)]
    else:
        lanes = [((blocks>>6)&3), ((blocks>>4)&3), ((blocks>>2)&3), (blocks&3)]
    vals = [lv[l] for l in lanes]
    imap = imap_linear(nc) if order=='linear' else imap_s16(nc, bs)
    D = np.empty((nblk, bs), dtype=np.float32)
    for p in range(4):
        D[:, imap[:,p]] = vals[p]
    R = ref.reshape(nblk, bs)
    # LS 最优 scale: s = sum(D*R)/sum(D^2)
    num = (D*R).sum(axis=1)
    den = (D*D).sum(axis=1)
    s = num/den
    # 重建
    pred = D * s[:,None]
    # 总体相关
    a = pred.reshape(-1); b = ref
    a=a-a.mean(); b=b-b.mean()
    r = (a*b).sum()/np.sqrt((a*a).sum()*(b*b).sum())
    sgn = (np.sign(pred.reshape(-1))==np.sign(ref)).mean()
    # scale 合理性: 应全部为正
    neg = (s<0).mean()
    return r, sgn, neg

base = [-1.5,-0.5,0.5,1.5]
perms = list(itertools.permutations(base))
results = []
for tag, (bs, nc) in CONFIGS.items():
    for order in ('linear','s16'):
        for bits in ('lsb','msb'):
            for perm in perms:
                r, sgn, neg = score(order, bits, perm, bs, nc)
                results.append((r, sgn, tag, order, bits, perm, neg))

results.sort(reverse=True)
print('===== top 25 (LS-scale 相关) =====')
for r, sgn, tag, order, bits, perm, neg in results[:25]:
    print('corr=%+.4f sgn=%.3f neg=%.2f %-10s order=%-6s bits=%-3s levels=%s' % (r, sgn, neg, tag, order, bits, perm))
