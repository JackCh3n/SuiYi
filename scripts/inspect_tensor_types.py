# -*- coding: utf-8 -*-
"""解析 GGUF 张量量化类型（对齐 llama.cpp gguf.cpp 的读取逻辑：dims 按 int64）"""
import struct, sys
from collections import Counter

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
    51: "IQ2_M_8_8", 52: "IQ2_M_4_4", 53: "IQ2_M_4_8", 56: "IQ3_S_XS",
}


def read_str(f):
    ln = struct.unpack("<Q", f.read(8))[0]
    return f.read(ln).decode("utf-8", errors="replace")


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


def main(path):
    with open(path, "rb") as f:
        assert f.read(4) == b"GGUF"
        version = struct.unpack("<I", f.read(4))[0]
        n_tensors = struct.unpack("<Q", f.read(8))[0]
        n_kv = struct.unpack("<Q", f.read(8))[0]
        print("GGUF v%d tensors=%d" % (version, n_tensors))
        for _ in range(n_kv):
            read_str(f)
            vtype = struct.unpack("<I", f.read(4))[0]
            skip_value(f, vtype)
        types = Counter()
        for i in range(n_tensors):
            name = read_str(f)
            n_dims = struct.unpack("<I", f.read(4))[0]
            dims = []
            for j in range(4):
                if j < n_dims:
                    dims.append(struct.unpack("<q", f.read(8))[0])
                else:
                    dims.append(1)
            ttype = struct.unpack("<I", f.read(4))[0]
            offset = struct.unpack("<Q", f.read(8))[0]
            types[ttype] += 1
            if ttype not in GGML_TYPE_NAMES or i < 3:
                print("  [%d] type %d = %-10s %s dims=%s" % (
                    i, ttype, GGML_TYPE_NAMES.get(ttype, "???"), name, dims))
        print("---- 汇总 ----")
        for ttype, cnt in types.most_common():
            print("  type %2d = %-10s x %d" % (ttype, GGML_TYPE_NAMES.get(ttype, "???"), cnt))


if __name__ == "__main__":
    main(sys.argv[1])
