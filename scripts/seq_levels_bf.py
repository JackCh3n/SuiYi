# -*- coding: utf-8 -*-
"""B512/start/s16/lsb 结构下，暴力搜索 24 种 level 排列，评估输出"""
import subprocess, os, itertools, re, sys

exe = r'd:\wwwroot\wwwroot\suiyi\build\hy-mt-rs\target\release\hy-mt.exe'
model = r'd:\wwwroot\wwwroot\suiyi\models\Hy-MT2-1.8B-2bit-v2.gguf'
base_env = dict(os.environ)
base_env['PATH'] = r'D:\xiao\mingw64\bin;' + base_env.get('PATH', '')
base_env['HY_MT_SEQ_BLOCK'] = '512'
base_env['HY_MT_SEQ_SCALEPOS'] = 'start'
base_env['HY_MT_SEQ_BITS'] = 'lsb'

def run(order, levels, timeout=90):
    e = dict(base_env)
    e['HY_MT_SEQ_ORDER'] = order
    e['HY_MT_SEQ_LEVELS'] = ','.join(str(x) for x in levels)
    cmd = [exe, 'translate', '--model', model, '--tgt', 'Chinese', '--instruction', '{text}',
           '--prompt', '将以下文本翻译为 中文，注意只需要输出翻译后的结果，不要额外解释：\n\nHello world',
           '--device', 'cpu', '--max-new-tokens', '12', '--temperature', '0.0']
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=e)
        out = r.stdout + r.stderr
        # 取最后一行（译文）
        lines = [l for l in out.splitlines() if l.strip() and not l.startswith('2026')]
        return (lines[-1] if lines else '').strip()
    except Exception as ex:
        return 'ERR ' + str(ex)

levels_base = [-1.5, -0.5, 0.5, 1.5]
perms = list(itertools.permutations(levels_base))
print('测试 %d 种 level 排列 x 2 种 order ...' % (len(perms)*2), flush=True)
results = []
for order in ('s16', 'linear'):
    for i, perm in enumerate(perms):
        out = run(order, list(perm))
        # 评估：输出是否像中文翻译（含汉字）
        han = sum(1 for ch in out if '\u4e00' <= ch <= '\u9fff')
        results.append((han, order, perm, out))
        if i % 6 == 0:
            print('  [%s] perm %d/24 ...' % (order, i), flush=True)

results.sort(key=lambda x: -x[0])
print('\n===== 含汉字最多的 top 12 =====')
for han, order, perm, out in results[:12]:
    print('han=%d order=%-5s levels=%s => %s' % (han, order, perm, out[:50]))
