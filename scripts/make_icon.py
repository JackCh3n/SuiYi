# -*- coding: utf-8 -*-
"""生成 SuiYi 随译 应用图标：512x512 PNG + 多尺寸 ICO + favicon
品牌色：蓝 #3370ff → 绿 #00b42a 渐变圆角方块 + 白色「译」字"""
from PIL import Image, ImageDraw, ImageFont, ImageFilter
import os, math

W = 512
SIZE = W
RADIUS = int(W * 0.225)  # 圆角半径
FONT_PATH = r"C:\Windows\Fonts\msyhbd.ttc"

def lerp(c1, c2, t):
    return tuple(int(a + (b - a) * t) for a, b in zip(c1, c2))

# ---- 1. 渐变背景（左上→右下：#3370ff → #00b42a） ----
C_TOP = (51, 112, 255)     # #3370ff
C_BOT = (0, 180, 42)       # #00b42a
img = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
px = img.load()
for y in range(SIZE):
    for x in range(SIZE):
        t = (x + y) / (2 * (SIZE - 1))  # 对角渐变
        px[x, y] = lerp(C_TOP, C_BOT, t) + (255,)

# ---- 2. 圆角遮罩 ----
mask = Image.new("L", (SIZE, SIZE), 0)
md = ImageDraw.Draw(mask)
md.rounded_rectangle([0, 0, SIZE - 1, SIZE - 1], radius=RADIUS, fill=255)
img.putalpha(mask)

# ---- 3. 绘制「译」字（白色粗体，居中略偏上） ----
draw = ImageDraw.Draw(img)
font = ImageFont.truetype(FONT_PATH, int(SIZE * 0.52))
text = "译"
# 用 textbbox 计算居中
bbox = draw.textbbox((0, 0), text, font=font)
tw, th = bbox[2] - bbox[0], bbox[3] - bbox[1]
tx = (SIZE - tw) // 2 - bbox[0]
ty = (SIZE - th) // 2 - bbox[1] - int(SIZE * 0.02)
# 先画深色投影增加层次，再画白色文字
draw.text((tx + 4, ty + 6), text, font=font, fill=(0, 80, 40, 120))
draw.text((tx, ty), text, font=font, fill=(255, 255, 255, 255))

# ---- 4. 顶部高光（柔和白色椭圆） ----
gloss = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
gd = ImageDraw.Draw(gloss)
gd.ellipse([-int(SIZE*0.15), -int(SIZE*0.30), int(SIZE*1.15), int(SIZE*0.28)], fill=(255, 255, 255, 26))
gloss = gloss.filter(ImageFilter.GaussianBlur(24))
img.alpha_composite(gloss)

# ---- 5. 右下双语角标（中 · EN 小圆点，呼应翻译） ----
# 两个小圆点：蓝底「文」+ 绿底「A」
dot_r = int(SIZE * 0.065)
for idx, (label, color) in enumerate([("文", (255, 255, 255)), ("A", (255, 255, 255))]):
    cx = SIZE - int(SIZE*0.13) - idx * int(SIZE*0.145)
    cy = SIZE - int(SIZE*0.12)
    # 圆点底（略透明白，叠在渐变上）
    dd = ImageDraw.Draw(img)
    dd.ellipse([cx - dot_r, cy - dot_r, cx + dot_r, cy + dot_r], fill=(255, 255, 255, 70))
    # 文字
    df = ImageFont.truetype(FONT_PATH, int(SIZE * 0.065))
    db = dd.textbbox((0, 0), label, font=df)
    dw2, dh2 = db[2] - db[0], db[3] - db[1]
    dd.text((cx - dw2/2 - db[0], cy - dh2/2 - db[1]), label, font=df, fill=color)

# 保存 PNG
out_png = r"d:\wwwroot\wwwroot\suiyi\gui\assets\appicon.png"
os.makedirs(os.path.dirname(out_png), exist_ok=True)
img.convert("RGB").save(out_png, "PNG")
print("saved", out_png, img.size)

# ---- 6. favicon（32x32 + 16x16） ----
for sz in (32, 16):
    fav = img.resize((sz, sz), Image.LANCZOS).convert("RGB")
    fav.save(rf"d:\wwwroot\wwwroot\suiyi\web\favicon-{sz}.png", "PNG")
    print("saved favicon", sz)

# ---- 7. ICO（多尺寸，供 Windows exe 资源） ----
ico_path = r"d:\wwwroot\wwwroot\suiyi\gui\assets\appicon.ico"
icon_sizes = [(256,256),(128,128),(64,64),(48,48),(32,32),(24,24),(16,16)]
img.convert("RGBA").save(ico_path, format="ICO", sizes=icon_sizes)
# 验证帧数
with Image.open(ico_path) as chk:
    print("saved", ico_path, "frames", getattr(chk, "n_frames", 1))
