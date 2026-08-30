# -*- coding: utf-8 -*-
"""精确定标 scale 因子"""
import subprocess, os

exe = r'd:\wwwroot\wwwroot\suiyi\build\hy-mt-rs\target\release\hy-mt.exe'
seq_model = r'd:\wwwroot\wwwroot\suiyi\models\Hy-MT2-1.8B-2bit-v2.gguf'
base_env = dict(os.environ)
base_env['PATH'] = r'D:\xiao\mingw64\bin;' + base_env.get('PATH', '')
base_env.update({'HY_MT_SEQ_BLOCK':'512','HY_MT_SEQ_SCALEPOS':'start','HY_MT_SEQ_ORDER':'linear',
                 'HY_MT_SEQ_BITS':'lsb','HY_MT_SEQ_SIGN':'up'})

def run(text, fac, timeout=150):
    e = dict(base_env); e['HY_MT_SEQ_SCALEFAC'] = str(fac)
    cmd = [exe, 'translate', '--model', seq_model, '--tgt', 'Chinese',
           '--instruction', 'Translate the following text into {tgt}, output only the translation:\n{text}',
           '--prompt', text, '--device', 'cpu', '--max-new-tokens', '30',
           '--temperature', '0.7', '--top-k', '20', '--top-p', '0.8', '--repeat-penalty', '1.2']
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=e)
        out = r.stdout + r.stderr
        lines = []
        for l in out.splitlines():
            s = l.strip()
            if not s: continue
            if s.startswith('2026') or s.startswith('\x1b') or ' INFO ' in s or ' WARN ' in s or ' ERROR ' in s:
                continue
            if s == text: continue
            lines.append(s)
        return '\n'.join(lines).strip()
    except Exception as ex:
        return 'ERR ' + str(ex)

text = 'I like to eat apples.'
target = '我喜欢吃苹果'
print('参考译文: %s' % target)
for fac in (1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 2.0, 2.2):
    out = run(text, fac)
    ok = 'OK' if (target[:3] in out or target in out) else '  '
    print('fac=%.1f %s => %s' % (fac, ok, out[:60]), flush=True)
