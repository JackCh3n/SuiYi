# -*- coding: utf-8 -*-
"""dump GGUF tensor_info 区域头部字节，人工确认量化类型字段"""
import struct, sys

def read_str(f):
    ln = struct.unpack("<Q", f.read(8))[0]
    return ln, f.read(ln)

def skip_value(f, vtype):
    if vtype in (0, 1, 7):
        f.read(1)
    elif vtype in (2, 3):
        f.read(2)
    elif vtype in (4, 5, 6):
        f.read(4)
    elif vtype == 8:
        read_str(f)
    elif vtype == 9:
        atype = struct.unpack("<I", f.read(4))[0]
        cnt = struct.unpack("<Q", f.read(8))[0]
        for _ in range(cnt):
            skip_value(f, atype)
    elif vtype in (10, 11, 12):
        f.read(8)

path = sys.argv[1]
with open(path, "rb") as f:
    assert f.read(4) == b"GGUF"
    version = struct.unpack("<I", f.read(4))[0]
    n_tensors = struct.unpack("<Q", f.read(8))[0]
    n_kv = struct.unpack("<Q", f.read(8))[0]
    for _ in range(n_kv):
        read_str(f)
        vtype = struct.unpack("<I", f.read(4))[0]
        skip_value(f, vtype)
    print("tensor_info 起始: %d" % f.tell())
    for i in range(min(6, n_tensors)):
        pos = f.tell()
        ln, raw = read_str(f)
        print("[%d] @%d name_len=%d name=%s" % (i, pos, ln, raw[:60]))
        n_dims = struct.unpack("<I", f.read(4))[0]
        print("    n_dims=%d" % n_dims)
        dims = [struct.unpack("<I", f.read(4))[0] for _ in range(n_dims)]
        print("    dims=%s" % dims)
        ttype = struct.unpack("<I", f.read(4))[0]
        offset = struct.unpack("<Q", f.read(8))[0]
        print("    type=%d offset=%d (0x%x)" % (ttype, offset, offset))