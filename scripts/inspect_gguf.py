# -*- coding: utf-8 -*-
"""解析 GGUF 文件头部，打印 metadata 与 tensor 量化类型"""
import struct
import sys
from collections import Counter

TYPE_NAMES = {
    0: "uint8", 1: "int8", 2: "uint16", 3: "int16", 4: "uint32",
    5: "int32", 6: "float32", 7: "bool", 8: "string", 9: "array",
    10: "uint64", 11: "int64", 12: "float64",
}

GGML_TYPE_NAMES = {
    0: "F32", 1: "F16", 2: "Q4_0", 3: "Q4_1", 6: "Q5_0", 7: "Q5_1",
    8: "Q8_0", 9: "Q8_1", 10: "Q2_K", 11: "Q3_K", 12: "Q4_K", 13: "Q5_K",
    14: "Q6_K", 15: "Q8_K", 16: "IQ2_XXS", 17: "IQ2_XS", 18: "IQ3_XXS",
    19: "IQ1_S", 20: "IQ4_NL", 21: "IQ3_S", 22: "IQ2_S", 23: "IQ4_XS",
    24: "I8", 25: "I16", 26: "I32", 27: "I64", 28: "F64", 29: "IQ1_M",
    30: "BF16", 32: "Q4_0_4_4", 33: "Q4_0_4_8", 34: "Q4_0_8_8",
    35: "TQ1_0", 36: "TQ2_0", 37: "IQ4_NL_4_4", 38: "IQ4_NL_4_8",
    39: "IQ4_NL_8_8", 40: "IQ3_S_8_8", 41: "IQ2_S_8_8", 42: "IQ2_S_4_4",
    43: "IQ2_S_4_8", 44: "IQ1_S_8_8", 45: "IQ1_S_4_4", 46: "IQ1_S_4_8",
    47: "IQ1_M_8_8", 48: "IQ1_M_4_4", 49: "IQ1_M_4_8", 50: "IQ2_M",
    51: "IQ2_M_8_8", 52: "IQ2_M_4_4", 53: "IQ2_M_4_8",
}


def read_str(f):
    ln = struct.unpack("<Q", f.read(8))[0]
    return f.read(ln).decode("utf-8", errors="replace")


def skip_value(f, vtype):
    if vtype == 0:  # uint8
        f.read(1)
    elif vtype == 1:  # int8
        f.read(1)
    elif vtype == 2:  # uint16
        f.read(2)
    elif vtype == 3:  # int16
        f.read(2)
    elif vtype == 4:  # uint32
        f.read(4)
    elif vtype == 5:  # int32
        f.read(4)
    elif vtype == 6:  # float32
        f.read(4)
    elif vtype == 7:  # bool
        f.read(1)
    elif vtype == 8:  # string
        read_str(f)
    elif vtype == 9:  # array
        atype = struct.unpack("<I", f.read(4))[0]
        cnt = struct.unpack("<Q", f.read(8))[0]
        for _ in range(cnt):
            skip_value(f, atype)
    elif vtype == 10:  # uint64
        f.read(8)
    elif vtype == 11:  # int64
        f.read(8)
    elif vtype == 12:  # float64
        f.read(8)
    else:
        raise ValueError("unknown kv type %d" % vtype)


def main(path):
    with open(path, "rb") as f:
        magic = f.read(4)
        assert magic == b"GGUF", "not gguf: %r" % magic
        version = struct.unpack("<I", f.read(4))[0]
        n_tensors = struct.unpack("<Q", f.read(8))[0]
        n_kv = struct.unpack("<Q", f.read(8))[0]
        print("GGUF version=%d tensors=%d kv_entries=%d" % (version, n_tensors, n_kv))
        print("---- metadata ----")
        for _ in range(n_kv):
            key = read_str(f)
            vtype = struct.unpack("<I", f.read(4))[0]
            val = "?"
            if vtype in (8,):  # string 照读
                val = '"%s"' % read_str(f)
            elif vtype in (0, 1, 2, 3, 4, 5, 6, 7, 10, 11, 12):
                fmt = {0: "<B", 1: "<b", 2: "<H", 3: "<h", 4: "<I", 5: "<i",
                       6: "<f", 7: "<?", 10: "<Q", 11: "<q", 12: "<d"}[vtype]
                val = struct.unpack(fmt, f.read(struct.calcsize(fmt)))[0]
            if vtype == 9:  # array：打印信息，不深入细节
                atype = struct.unpack("<I", f.read(4))[0]
                cnt = struct.unpack("<Q", f.read(8))[0]
                print("  %s [array<%s> x%d]" % (key, TYPE_NAMES.get(atype, "?"), cnt))
                elem = atype
                for _ in range(cnt):
                    skip_value(f, elem)
                continue
            print("  %s [%s] = %s" % (key, TYPE_NAMES.get(vtype, "?"), val))
        print("---- tensor types ----")
        print("  tensor_info 起始偏移: %d" % f.tell())
        types = Counter()
        for i in range(n_tensors):
            try:
                name = read_str(f)
                n_dims = struct.unpack("<I", f.read(4))[0]
                # dims
                for _ in range(n_dims):
                    f.read(4)
                ttype = struct.unpack("<I", f.read(4))[0]
                offset = struct.unpack("<Q", f.read(8))[0]
            except struct.error:
                print("  解析失败于第 %d 个 tensor（偏移 %d），需要更大读取缓冲区" % (i, f.tell()))
                # 尝试直接读取原始字段：name 长度前 8 字节
                break
            types[ttype] += 1
            if i < 8 or ttype not in GGML_TYPE_NAMES:
                print("  [%d] type %d = %-14s %s" % (i, ttype, GGML_TYPE_NAMES.get(ttype, "???"), name))
        for ttype, cnt in types.most_common():
            print("  type %d = %-14s x %d" % (ttype, GGML_TYPE_NAMES.get(ttype, "???"), cnt))


if __name__ == "__main__":
    main(sys.argv[1])