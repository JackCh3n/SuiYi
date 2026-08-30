# -*- coding: utf-8 -*-
"""重跑 SEQ 参数搜索（修复排序），实时输出，先测 B512 组"""
import subprocess, os, re, sys, time

exe = r'd:\wwwroot\wwwroot\suiyi\build\hy-mt-rs\target\release\hy-mt.exe'
model = r'd:\wwwroot\wwwroot\suiyi\models\Hy-MT2-1.8B-2bit-v2.gguf'
base_env = dict(os.environ)
base_env['PATH'] = r'D:\xiao\mingw64\bin;' + base_env.get('PATH', '')

def run(env_extra, timeout=120):
    e = dict(base_env); e.update(env_extra)
    cmd = [exe, 'debug-forward', '--model', model, '--chat-translate', 'Chinese',
           '--prompt', 'Hi', '--top-k', '2', '--decode-steps', '1', '--device', 'cpu']
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=e)
        out = r.stdout + r.stderr
        gen = ''
        for line in out.splitlines():
            if line.startswith('Generated:'):
                gen = line.replace('Generated:', '').strip()
        nums = re.findall(r'id=\d+\s+([0-9.eE+-]+)', out)
        vals = sorted((float(v) for v in nums[:6]), reverse=True) if nums else []
        top1 = vals[0] if vals else None
        top2 = vals[1] if len(vals) > 1 else None
        margin = (top1 - top2) if (top1 is not None and top2 is not None) else -1
        return gen, margin, top1
    except Exception as ex:
        return '', -1, None

combos = []
for pos in ('start', 'end'):
    for order in ('linear', 's16'):
        for bits in ('lsb', 'msb'):
            for sign in ('up', 'down'):
                combos.append(('B512', {'HY_MT_SEQ_BLOCK': '512', 'HY_MT_SEQ_SCALEPOS': pos,
                                        'HY_MT_SEQ_ORDER': order, 'HY_MT_SEQ_BITS': bits,
                                        'HY_MT_SEQ_SIGN': sign}))
# 也补测几个 B256 的高 div
for order in ('linear', 's16'):
    for bits in ('lsb', 'msb'):
        combos.append(('B256-div1', {'HY_MT_SEQ_BLOCK': '256', 'HY_MT_SEQ_ORDER': order,
                                     'HY_MT_SEQ_BITS': bits, 'HY_MT_SEQ_SIGN': 'up', 'HY_MT_SEQ_DIV': '1'}))
        combos.append(('B256-div0.1', {'HY_MT_SEQ_BLOCK': '256', 'HY_MT_SEQ_ORDER': order,
                                       'HY_MT_SEQ_BITS': bits, 'HY_MT_SEQ_SIGN': 'up', 'HY_MT_SEQ_DIV': '0.1'}))

results = []
for i, (tag, env) in enumerate(combos):
    t0 = time.time()
    gen, margin, top1 = run(env)
    dt = time.time() - t0
    results.append((margin, tag, env, gen, top1))
    print('[%d/%d %.0fs] margin=%.2f top1=%.4g %-12s %s => %s' % (i, len(combos), dt, margin, top1 or 0, tag, env, gen[:50]), flush=True)

results.sort(key=lambda x: -x[0])
print('\n===== top 10 =====')
for margin, tag, env, gen, top1 in results[:10]:
    print('margin=%.2f %-12s %s => %s' % (margin, tag, env, gen[:50]))
