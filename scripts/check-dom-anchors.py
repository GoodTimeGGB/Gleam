#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""DOM 锚点与函数都要两头接上：JS 引用的 id 必须存在，新面板的锚点必须有人引用，定义的函数必须有人调用。

**为什么要有**：这个仓库栽过四次的是同一类——**判据对、线没接**。前端版本更隐蔽：
`$('#ro-memory')` 取到 `null` 不会抛错（本项目的 `$` 找不到就返回 null，各处都写了
`if (el)` 兜底），于是那一格读数永远停在 HTML 里写死的「—」或「0」。界面在、数据没接，
渲染照常，测试照常绿，只有用户盯着那个不动的数字时才发现。

**三条判据**（前两条方向相反，第三条管的是 JS 自己内部的接线）：
1. 正向（全仓）：app.js 里 `$('#x')` / `getElementById('x')` / `querySelector('#x')`
   引用的 id，必须在 index.html 或 app.js 自己的模板字符串里被定义。
2. 反向（管现场栏 `rail-*` / `ro-*`、输入区就地控件 `cp-*`、预览面板 `bp-*`
   与连接台账 `cx-*`）：
   这些锚点必须被 app.js
   引用，或被 HTML 的
   `aria-controls` / `aria-labelledby` / `aria-describedby` / `for` 指到（那些是给
   辅助技术的，本来就不该有 JS 生产者）。
   **为什么不全仓反向**：index.html 里有 38 个 id 是合法的无人引用（视图容器、CSS 钩子、
   aria 目标），全查就得维护一份白名单——白名单会漂，漂了之后这条判据只是在点头。
   这几片是新加的、一共就这三小片，反向在这里查得住，也比那里更值得查。
   `cp-` 是批次 F12 起加的：水位条与模型芯片都把占位写在 HTML 里（「—」「模型」），
   少接一条线就是那一格停着不动、测试全绿。
   `bp-` 是批次 F13 起加的：面板里「上一页 / 下一页」的可用态全由 JS 算，HTML 先画出来
   而 JS 没接，就是一个按下去什么都不发生的按钮。
   `cx-` 是批次 F16 起加的：连接与出网台账的表体与范围文案全由 JS 填，HTML 里只有那个
   「—」。这一屏是用户判断「我到底允许了什么」的唯一入口，空着比缺这一屏更糟。
3. JS 内部（批次 F14 起加）：app.js 里 `function x() {}` 定义出来的函数，必须被按名字
   引用至少一次（调用、当回调传出去、放进对象字面量都算）。
   **为什么单加这一条**：`renderChanges` 就是这么漏过去的——函数写好了、注释也写了、
   Go 侧的接线还有五条变异钉着，唯独卡片上没人调它，于是 README 那句"任务卡上列"是空的。
   前两条都只看 id，一个不引用任何 id 的渲染函数从它们中间穿过去毫无痕迹。
   **数引用前先剥注释**：否则"renderChanges 渲染本次改动"这样一句注释，就把死函数洗成了活的。

**怎么改才通过**：锚点两边同名。改了 HTML 的 id 就同时改 JS 的引用；
新面板格子要么接上生产者，要么先别画（画一个永远不动的格子比不画更糟）；
函数要么在它的宿主入口里调起来，要么删掉（留着就是给下一个人留一个"以为有了"的坑）。

**自我失效的防线**：读不到两个文件之一就失败；现场栏一个锚点都找不到也失败——
宁可红着，也不给"已通过"的错觉。

**负例控制自带**：`--self-test` 用真文本现造五份坏文本（JS 引用了不存在的锚点 /
面板格子、输入区格子、预览面板按钮没人接、函数没有任何调用方），断言本脚本会把它们判红。

用法：python scripts/check-dom-anchors.py [--self-test] [仓库根目录]
退出码：0 = 三处都接上（`--self-test` 时：五份负例都被拦住）；1 = 否则。
"""
import io
import os
import re
import sys

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

APP_JS = os.path.join("internal", "webui", "static", "app.js")
INDEX_HTML = os.path.join("internal", "webui", "static", "index.html")

# 反向判据罩住的锚点前缀：rail-* 现场栏结构、ro-* 读数格子、cp-* 输入区就地控件、
# bp-* 浏览器预览面板（批次 F13）、cx-* 连接与出网台账（批次 F16）
SIDE_PREFIX = ("rail-", "ro-", "cp-", "bp-", "cx-")
# HTML 里指向 id 的无障碍属性（这些算"有人接"，只是接的人不是 JS）
ARIA_TO = ("aria-controls", "aria-labelledby", "aria-describedby", "for")

REF_RE = re.compile(r"""(?:\$\(\s*|getElementById\(\s*|querySelector\(\s*)['"]#([A-Za-z0-9_\-]+)['"]""")
ID_ATTR_RE = re.compile(r'id="([A-Za-z0-9_\-]+)"')
ID_ASSIGN_RE = re.compile(r"""\.id\s*=\s*['"]([A-Za-z0-9_\-]+)['"]""")
FUNC_RE = re.compile(r"^\s*(?:async\s+)?function\s+([A-Za-z0-9_$]+)\s*\(", re.M)


def strip_comments(text):
    """去掉注释再数引用。

    不去注释的话，一句「renderChanges 渲染本次改动」就算一次调用——而这条判据要拦的
    恰恰是**函数写好了、注释也写了、就是没人走它**。
    """
    text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
    return "\n".join("" if re.match(r"^\s*(//|\*|/\*)", ln) else ln
                     for ln in text.split("\n"))


def dead_functions(js_text):
    """定义了却没被按名字引用一次的函数（定义本身算一次，所以判 <2）。

    调用、作为回调传出去、放进对象字面量都算引用。只认代码里的裸名字，不认字符串与
    拼接——那条路太窄，宁可少判也不维护一份"哪些名字可以死着"的名单。
    """
    code = strip_comments(js_text)
    return sorted(name for name in FUNC_RE.findall(code)
                  if len(re.findall(r"(?<![\w.$#])%s(?![\w$])" % re.escape(name), code)) < 2)


def read(path):
    with io.open(path, "rb") as f:
        return f.read().decode("utf-8", "replace")


def refs_of(js_text):
    """JS 里的 DOM 锚点引用。拼接出来的（`'#view-' + name`）不算：它没有指名某个锚点。"""
    out = set()
    for m in REF_RE.finditer(js_text):
        tail = js_text[m.end():m.end() + 8].lstrip()
        if tail.startswith("+"):
            continue
        out.add(m.group(1))
    return out


def judge(js_text, html_text, label_js, label_html, say):
    """判定一份 (app.js, index.html) 文本。返回退出码。"""
    defined = set(ID_ATTR_RE.findall(html_text)) | set(ID_ATTR_RE.findall(js_text)) | set(ID_ASSIGN_RE.findall(js_text))
    refs = refs_of(js_text)

    rc = 0
    missing = sorted(r for r in refs if r not in defined)
    if missing:
        say("%s 里引用了 index.html 与本文件模板都没有的 DOM 锚点（取到 null，那一格会永远停在写死的占位上）：" % label_js)
        for m in missing[:20]:
            say("  %s: #%s" % (label_js, m))
        if len(missing) > 20:
            say("  …另有 %d 处" % (len(missing) - 20))
        rc = 1

    aria_targets = set()
    for attr in ARIA_TO:
        for value in re.findall(r'%s="([^"]*)"' % attr, html_text):
            aria_targets.update(value.split())
    side_ids = sorted(i for i in set(ID_ATTR_RE.findall(html_text)) if i.startswith(SIDE_PREFIX))
    if not side_ids:
        say("index.html 里找不到任何 %s 锚点：现场栏被删了还是改了前缀？判据失效，请先修本脚本。"
            % "/".join(SIDE_PREFIX))
        return 1
    orphans = [i for i in side_ids if ("#" + i) not in js_text and i not in aria_targets]
    if orphans:
        say("这些锚点没有任何生产者（界面画出来了，那一格永远是占位）：")
        for o in orphans:
            say("  %s: id=\"%s\"" % (label_html, o))
        rc = 1

    dead = dead_functions(js_text)
    if dead:
        say("这些函数定义了却没有任何调用方（写好了、注释也写了，就是没人走它——"
            "于是那一块界面永远不出现，而仓里全是绿）：")
        for name in dead[:20]:
            say("  %s: function %s()" % (label_js, name))
        if len(dead) > 20:
            say("  …另有 %d 个" % (len(dead) - 20))
        rc = 1

    if rc == 0:
        say("  %d 处 JS 锚点引用全部有定义；现场栏、输入区与预览面板 %d 个锚点全部有人接；%d 个函数全部有人调用"
            % (len(refs), len(side_ids), len(FUNC_RE.findall(strip_comments(js_text)))))
    return rc


def self_test(js_text, html_text) -> int:
    """拿真文本现造五份坏文本，断言判据会把它们拦住。"""
    if not any(i.startswith(SIDE_PREFIX) for i in ID_ATTR_RE.findall(html_text)):
        print("self-test：真文本里没有现场栏/输入区/预览面板锚点，负例无从构造")
        return 1
    cases = [
        ("JS 引用了不存在的锚点", js_text + "\nconst probe = $('#rail-anchor-that-is-gone');\n", html_text),
        ("面板格子没人接", js_text, html_text.replace('<dl class="readout">', '<dl class="readout">\n<div class="readout-row"><dt>孤儿</dt><dd id="ro-orphan">0</dd></div>', 1)),
        ("输入区格子没人接", js_text, html_text.replace('<div class="cp-model-list" id="cp-model-list"', '<div id="cp-orphan">0</div>\n<div class="cp-model-list" id="cp-model-list"', 1)),
        ("预览面板按钮没人接", js_text, html_text.replace('<div class="bp-quick" id="bp-quick"', '<button id="bp-orphan" type="button">孤儿</button>\n<div class="bp-quick" id="bp-quick"', 1)),
        # 台账的表体与范围文案都是 JS 填的：HTML 先画出空壳、app.js 没接，
        # 这一屏就永远是那个「—」——而它恰恰是用户判断"我有没有被交代清楚"的那一屏。
        ("台账格子没人接", js_text, html_text.replace('<div id="cx-conn-list"', '<p id="cx-orphan">—</p>\n<div id="cx-conn-list"', 1)),
        # 只加一个函数定义、外面一句调用都不给。真出过这一类：`renderChanges` 写好了、
        # 注释也写了，卡片上那个槽位却永远是空的——因为没人调它。
        ("函数没有任何调用方", js_text + "\nfunction renderAnchorThatNobodyCalls(t) { return t; }\n", html_text),
    ]
    bad = 0
    for name, js, html in cases:
        sink = []
        rc = judge(js, html, "app.js", "index.html", sink.append)
        if rc == 0:
            print("负例没被拦住：%s —— 判据在永远点头，本脚本要修" % name)
            bad += 1
        else:
            print("  拦住：%s（%s）" % (name, sink[0][:56]))
    good = []
    if judge(js_text, html_text, "app.js", "index.html", good.append) != 0:
        print("真文本被判红，self-test 无从对照：\n  " + "\n  ".join(good))
        return 1
    return 1 if bad else 0


def main() -> int:
    argv = [a for a in sys.argv[1:] if a != "--self-test"]
    selftest = "--self-test" in sys.argv[1:]
    root = argv[0] if argv else "."
    try:
        js_text, html_text = read(os.path.join(root, APP_JS)), read(os.path.join(root, INDEX_HTML))
    except OSError as e:
        print("读不到前端源文件：%s" % e)
        return 1
    label_js = APP_JS.replace("\\", "/")
    label_html = INDEX_HTML.replace("\\", "/")
    rc = judge(js_text, html_text, label_js, label_html, print)
    if rc != 0 or not selftest:
        return rc
    return self_test(js_text, html_text)


if __name__ == "__main__":
    sys.exit(main())
