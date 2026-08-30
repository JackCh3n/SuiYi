#!/usr/bin/env python3
# 将 Hy-MT2-1.8B-1.25bit-v2.gguf 的张量类型 ID 从 40 改写为 43（STQ1_0），
# 使其能被 llama.cpp PR #22836（STQ1_0=type 43，同 84B/512 布局）加载。
# 仅修改类型 ID，数据与偏移不变；输出到新文件，不覆盖原文件。
import struct, sys, os

def read_str(f):
    l = struct.unpack('<Q', f.read(8))[0]
    return f.read(l).decode('utf-8', 'replace')

# GGUF value 类型大小（不含字符串/数组）
def skip_value(f, t):
    if t in (0, 1, 7):    # uint8/int8/bool
        f.seek(1, 1)
    elif t in (2, 3):     # uint16/int16
        f.seek(2, 1)
    elif t in (4, 5, 6, 9): # uint32/int32/float32/... (9 is array marker, handled separately)
        f.seek(4, 1)
    elif t == 8:          # string
        read_str(f)
    elif t == 10:         # array: u64 count + u32 elem_type + elements
        n = struct.unpack('<Q', f.read(8))[0]
        et = struct.unpack('<I', f.read(4))[0]
        for _ in range(n):
            skip_value(f, et)
    elif t in (11, 12):   # uint64/int64
        f.seek(8, 1)
    elif t == 13:         # float64
        f.seek(8, 1)
    else:
        raise Exception('unknown gguf type %d' % t)

src = 'models/Hy-MT2-1.8B-1.25bit-v2.gguf'
dst = 'models/Hy-MT2-1.8B-1.25bit-v2.fixed.gguf'

with open(src, 'rb') as f:
    data = f.read()

off = 0
def u32():
    global off
    v = struct.unpack_from('<I', data, off)[0]; off += 4; return v
def u64():
    global off
    v = struct.unpack_from('<Q', data, off)[0]; off += 8; return v
def s():
    global off
    l = struct.unpack_from('<Q', data, off)[0]; off += 8
    s2 = data[off:off+l]; off += l; return s2.decode('utf-8','replace')
def skiptype(t):
    global off
    if t in (0,1,7): off += 1
    elif t in (2,3): off += 2
    elif t in (4,5,6): off += 4
    elif t == 8: s()
    elif t == 9:
        # GGUF 数组：元素类型 u32 + 数量 u64 + 元素
        et = u32(); n = u64()
        for _ in range(n): skiptype(et)
    elif t in (10,11,12): off += 8
    else: raise Exception('unknown type %d'%t)

magic = data[0:4]
assert magic == b'GGUF', 'not gguf'
off = 4
ver = u32()
n_tensors = u64()
n_kv = u64()
print('version=%d tensors=%d kv=%d' % (ver, n_tensors, n_kv))

for _ in range(n_kv):
    s()                       # key
    t = u32()                 # value type
    skiptype(t)               # value

# 定位每个张量的 type 字段偏移
type_positions = []
for _ in range(n_tensors):
    s()                       # name
    nd = u32()
    off += 8 * nd             # dims
    type_pos = off            # type u32 field position
    t = u32()
    type_positions.append((type_pos, t))
    off += 8                  # offset

print('tensor type histogram:', {t: type_positions.count((p,t)) for p,t in type_positions})
print('tensor count parsed:', len(type_positions))

# 检查非 40 的张量类型
others = set(t for _,t in type_positions if t != 40)
print('non-40 types present:', others)

# 改写 40 -> 43
out = bytearray(data)
changed = 0
for pos, t in type_positions:
    if t == 40:
        struct.pack_into('<I', out, pos, 43)
        changed += 1
print('patched type 40 -> 43 count:', changed)

with open(dst, 'wb') as f:
    f.write(out)
print('saved', dst, os.path.getsize(dst), 'bytes')
