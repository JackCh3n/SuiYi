# -*- coding: utf-8 -*-
"""批量测试 SEQ 参数组合（order/sign/div），跑 debug-forward 抓 Generated 结果"""
import subprocess, os, itertools, sys

exe = r'd:\wwwroot\wwwroot\suiyi\build\hy-mt-rs\target\release\hy-mt.exe'
model = r'd:\wwwroot\wwwroot\suiyi\models\Hy-MT2-1.8B-2bit-v2.gguf'

env = dict(os.environ)
env['PATH'] = r'D:\xiao\mingw64\bin;' + env.get('PATH', '')

def run(order, sign, div):
    e = dict(env)
    e['HY_MT_SEQ_ORDER'] = order
    e['HY_MT_SEQ_SIGN'] = sign
    e['HY_MT_SEQ_DIV'] = str(div)
    cmd = [exe, 'debug-forward', '--model', model, '--chat-translate', 'Chinese',
           '--prompt', 'Hello world', '--top-k', '3', '--decode-steps', '2', '--device', 'cpu']
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=180, env=e)
        out = r.stdout + r.stderr
        gen = ''
        for line in out.splitlines():
            if line.startswith('Generated:'):
                gen = line
        return gen, ''
    except Exception as ex:
        return '', str(ex)

combos = []
for order in ('linear', 's16'):
    for sign in ('up', 'down'):
        for div in (255, 170, 128):
            combos.append((order, sign, div))

for (o, s, d) in combos:
    gen, err = run(o, s, d)
    print('order=%-6s sign=%-4s div=%-3s => %s' % (o, s, d, gen or ('ERR ' + err)))
