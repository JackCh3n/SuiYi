# -*- coding: utf-8 -*-
"""全面暴力搜索 SEQ 参数组合，用 debug-forward 的 logit 置信度评估"""
import subprocess, os, re, sys

exe = r'd:\wwwroot\wwwroot\suiyi\build\hy-mt-rs\target\release\hy-mt.exe'
model = r'd:\wwwroot\wwwroot\suiyi\models\Hy-MT2-1.8B-2bit-v2.gguf'
base_env = dict(os.environ)
base_env['PATH'] = r'D:\xiao\mingw64\bin;' + base_env.get('PATH', '')

def run(env_extra, timeout=90):
    e = dict(base_env)
    e.update(env_extra)
    cmd = [exe, 'debug-forward', '--model', model, '--chat-translate', 'Chinese',
           '--prompt', 'Hi', '--top-k', '2', '--decode-steps', '1', '--device', 'cpu']
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=e)
        out = r.stdout + r.stderr
        gen = ''
        top1 = top2 = None
        for line in out.splitlines():
            if line.startswith('Generated:'):
                gen = line.replace('Generated:', '').strip()
        # 找第一步的 top logit 值（数字）
        nums = re.findall(r'id=\d+\s+([0-9.eE+-]+)', out)
        if nums:
            vals = sorted((float(v) for v in nums[:6]), reverse=True)
            top1 = vals[0] if vals else None
            top2 = vals[1] if len(vals) > 1 else None
        margin = (top1 - top2) if (top1 is not None and top2 is not None) else -1
        return gen, margin
    except Exception as ex:
        return '', -1

combos = []
# Block 256 (65B, u8 scale)
for order in ('linear', 's16'):
    for bits in ('lsb', 'msb'):
        for sign in ('up', 'down'):
            for div in (1, 16, 32, 64, 128, 170, 255, 512, 1024):
                combos.append(('B256', {'HY_MT_SEQ_BLOCK': '256', 'HY_MT_SEQ_ORDER': order,
                                        'HY_MT_SEQ_BITS': bits, 'HY_MT_SEQ_SIGN': sign,
                                        'HY_MT_SEQ_DIV': str(div)}))
# Block 512 (130B, f16 scale)
for pos in ('start', 'end'):
    for order in ('linear', 's16'):
        for bits in ('lsb', 'msb'):
            for sign in ('up', 'down'):
                combos.append(('B512', {'HY_MT_SEQ_BLOCK': '512', 'HY_MT_SEQ_SCALEPOS': pos,
                                        'HY_MT_SEQ_ORDER': order, 'HY_MT_SEQ_BITS': bits,
                                        'HY_MT_SEQ_SIGN': sign}))

results = []
for i, (tag, env) in enumerate(combos):
    gen, margin = run(env)
    results.append((margin, tag, env, gen))
    if i % 10 == 0:
        print('  progress %d/%d, best margin so far: %.2f' % (i, len(combos), max(r[0] for r in results)))

results.sort(reverse=True)
print('\n===== 按 logit 置信度排序 (top 15) =====')
for margin, tag, env, gen in results[:15]:
    print('margin=%.2f %-5s %s => %s' % (margin, tag, env, gen[:60]))
