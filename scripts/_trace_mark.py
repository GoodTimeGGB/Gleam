"""一次性工具：把参考 PNG 的折角标记描成矢量，写进 4 个 svg 与 make-icon.go 的 markPath。

做法：取 mark 掩码的每条「边界单位边」，首尾相接成闭合折线（像素级精确），
再用 Douglas–Peucker 抽稀到 ~140 点。低分辨率图的抗锯齿只会让边界差半个像素，
所以不做平滑（平滑会把内凹尖角填圆，反而更不像）。
只跑一次，不进构建。用法：python scripts/_trace_mark.py <参考PNG>
"""
import re
import sys
import numpy as np
from PIL import Image, ImageDraw

if len(sys.argv) < 2:
    sys.exit("用法：python scripts/_trace_mark.py <品牌方给的图标PNG>")
SRC = sys.argv[1]
OUT = 1024.0

im = Image.open(SRC).convert("RGBA")
arr = np.asarray(im).astype(int)
alpha, lum = arr[:, :, 3], arr[:, :, :3].mean(axis=2)
mask = (alpha > 128) & (lum > 90)
H, W = mask.shape
M = np.zeros((H + 2, W + 2), bool)
M[1:-1, 1:-1] = mask

edges = {}


def add(a, b):
    edges.setdefault(a, []).append(b)


ys, xs = np.nonzero(M)
for y, x in zip(ys, xs):
    if not M[y - 1, x]: add((x, y), (x + 1, y))
    if not M[y, x + 1]: add((x + 1, y), (x + 1, y + 1))
    if not M[y + 1, x]: add((x + 1, y + 1), (x, y + 1))
    if not M[y, x - 1]: add((x, y + 1), (x, y))

start = min(edges)
loop, cur = [start], start
while True:
    nxts = edges.get(cur)
    if not nxts:
        break
    nxt = nxts.pop(0)
    loop.append(nxt)
    cur = nxt
    if cur == start:
        break
loop = loop[:-1]


def rdp(pts, eps):
    if len(pts) < 3:
        return list(pts)
    p0, p1 = np.array(pts[0], float), np.array(pts[-1], float)
    line = p1 - p0
    L = float(np.hypot(*line))
    if L == 0:
        d = [float(np.hypot(*(np.array(p, float) - p0))) for p in pts]
    else:
        d = [abs(float(line[0] * (p[1] - p0[1]) - line[1] * (p[0] - p0[0]))) / L for p in pts]
    i = int(np.argmax(d))
    if d[i] > eps:
        return rdp(pts[: i + 1], eps)[:-1] + rdp(pts[i:], eps)
    return [pts[0], pts[-1]]


poly = rdp(loop, 1.0)
if poly[0] == poly[-1]:
    poly = poly[:-1]

k = OUT / W
pts = [(round(x * k, 1), round(y * k, 1)) for x, y in poly]
d = " ".join(("M" if i == 0 else "L") + ("%g %g" % p) for i, p in enumerate(pts)) + "Z"

img = Image.new("L", (W, H), 0)
ImageDraw.Draw(img).polygon([(float(x), float(y)) for x, y in poly], fill=255)
iou = np.logical_and(np.asarray(img) > 127, mask).sum() / np.logical_or(np.asarray(img) > 127, mask).sum()
print("pts %d  IoU %.4f  bytes %d" % (len(pts), iou, len(d)))

# ---- 圆角方块半径 ----
# 左上角的弧是一个过原点、圆心在 (r, r)、半径 r 的圆；弧上任一点 (x, y) 满足
# (x-r)^2 + (y-r)^2 = r^2，解出 r = (x+y) + sqrt(2xy)。逐点取值再取中位数，
# 比整段最小二乘稳（直边与抗锯齿会把拟合拽偏）。
sq = alpha > 128
corner_pts = []
for x in range(1, 200):
    col = np.nonzero(sq[:, x])[0]
    if len(col) and col[0] > 3:
        corner_pts.append((x, int(col[0])))
for y in range(1, 200):
    row = np.nonzero(sq[y])[0]
    if len(row) and row[0] > 3:
        corner_pts.append((int(row[0]), y))
cp = np.array(corner_pts, float)
rads = (cp[:, 0] + cp[:, 1]) + np.sqrt(2 * cp[:, 0] * cp[:, 1])
rads = rads[(rads > 60) & (rads < 200)]
rad = float(np.median(rads))
ratio = round(rad / W, 4)
print("corner radius %.1f src px -> %.4f of side" % (rad, ratio))

XS = [p[0] for p in pts]
YS = [p[1] for p in pts]
side = 680.0
vb = "%.0f %.0f %.0f %.0f" % (
    round((min(XS) + max(XS)) / 2 - side / 2), round((min(YS) + max(YS)) / 2 - side / 2), side, side)

LOGO = ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">\n'
        '  <title>Gleam</title>\n'
        '  <rect width="1024" height="1024" rx="%d" fill="#0b0b0a"/>\n'
        '  <path fill="#d7fb58" d="%s"/>\n</svg>\n' % (round(rad * OUT / W), d))
SQUARE = ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">\n'
          '  <title>Gleam</title>\n'
          '  <rect width="1024" height="1024" fill="#0b0b0a"/>\n'
          '  <path fill="#d7fb58" d="%s"/>\n</svg>\n' % d)
MARK = ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="%s" width="24" height="24" fill="currentColor">\n'
        '  <title>Gleam</title>\n'
        '  <path d="%s"/>\n</svg>\n' % (vb, d))

for p in ("assets/gleam-logo.svg", "internal/webui/static/gleam-logo.svg"):
    open(p, "w", encoding="utf-8").write(LOGO)
open("assets/gleam-logo-square.svg", "w", encoding="utf-8").write(SQUARE)
open("assets/gleam-mark.svg", "w", encoding="utf-8").write(MARK)
print("wrote 4 svg")

# ---- 回写 make-icon.go：markPath + cornerRatio ----
g = open("scripts/make-icon.go", encoding="utf-8").read()
g = re.sub(r'const markPath = "[^"]*"', 'const markPath = "%s"' % d, g)
g = re.sub(r'const cornerRatio = [0-9.]+', 'const cornerRatio = %s' % ratio, g)
open("scripts/make-icon.go", "w", encoding="utf-8").write(g)
print("patched make-icon.go (markPath + cornerRatio %s)" % ratio)
