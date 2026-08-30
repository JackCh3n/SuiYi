# -*- coding: utf-8 -*-
"""全面测试: 多方向 + 长文本"""
import subprocess, os, sys

exe = r'd:\wwwroot\wwwroot\suiyi\build\hy-mt-rs\target\release\hy-mt.exe'
seq_model = r'd:\wwwroot\wwwroot\suiyi\models\Hy-MT2-1.8B-2bit-v2.gguf'

def run(tgt, text, temp=0.7, timeout=300):
    cmd = [exe, 'translate', '--model', seq_model, '--tgt', tgt,
           '--instruction', '{text}',
           '--prompt', '将以下文本翻译为 %s，注意只需要输出翻译后的结果，不要额外解释：\n\n%s' % (tgt, text),
           '--device', 'cpu', '--max-new-tokens', str(max(64, len(text))), '--temperature', str(temp)]
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        out = r.stdout + r.stderr
        lines = []
        for l in out.splitlines():
            s = l.strip()
            if not s: continue
            if s.startswith('2026') or s.startswith('\x1b') or ' INFO ' in s or ' WARN ' in s or ' ERROR ' in s:
                continue
            lines.append(s)
        return '\n'.join(lines).strip()
    except Exception as ex:
        return 'ERR ' + str(ex)

tests = [
    ('Chinese', 'Technology is advancing rapidly and changing our daily lives.'),
    ('Chinese', 'She studied hard and passed the exam with flying colors.'),
    ('English', '今天是个好日子，阳光明媚，适合外出游玩。'),
    ('Chinese', 'The cat sat on the windowsill, watching the birds outside.'),
    ('English', '我们公司致力于为全球客户提供优质的产品和服务。'),
]
for tgt, text in tests:
    out = run(tgt, text)
    print('%-8s => %s' % (tgt, out[:120]), flush=True)

# 长文本测试
long_text = ('Artificial intelligence (AI) is transforming industries around the world. '
             'From healthcare to finance, AI systems are helping humans make better decisions '
             'and work more efficiently. In medicine, AI can analyze medical images to detect '
             'diseases early. In business, it can predict market trends and optimize supply chains. '
             'However, AI also brings challenges, including privacy concerns and job displacement. '
             'It is important for society to develop AI responsibly, ensuring that the benefits '
             'are shared fairly while minimizing the risks.')
print('\n长文本:')
out = run('Chinese', long_text, timeout=600)
print(out)
