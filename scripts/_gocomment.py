#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""读 Go 源码判据前的准备：**把注释换成空格，字符串原样留着。**

**为什么要有**：仓里两道闸门（`check-egress-owner.py`、`check-cue-owner.py`）都要在 Go
源码里找 `SomeCall(` 这种"接线证据"，也都要判"字符串字面量里不许出现某段文案"。
而这两件事都会被注释干扰：注释本来就该提那些函数名、本来就该用 `**` 标重点。
不剥注释，判据第一天就红在满屏假告警上；红了三天的判据，第四天就被人关掉了。

**为什么用字符状态机而不是正则**：`"https://x"` 里的 `//` 也是斜杠。一刀切下去，
那行剩下的部分——通常正是真实的那次调用——会被一起吃掉，而**漏判的方向是"通过"**。
所以这里认四件事：行注释、块注释、双引号串（含转义）、反引号原始串与 rune。

**边界**：只处理注释与字符串的边界，不做词法分析（不认 `/*` 出现在字符串里之外的怪异
嵌套——Go 本身就不允许）。输出保持行号与列位不变，报错时指到原文件的行才有意义。

用法：`import _gocomment` 后 `_gocomment.strip(src)`（同目录，无需处理 sys.path）。
"""


def strip(text):
    """把 Go 源码里的注释替换成空格；字符串与其余字符原位保留。"""
    out = []
    i, n = 0, len(text)
    state = ""  # "" | line | block | string | raw | rune
    while i < n:
        c = text[i]
        nxt = text[i + 1] if i + 1 < n else ""
        if state == "":
            if c == "/" and nxt == "/":
                state = "line"
                out.append("  ")
                i += 2
                continue
            if c == "/" and nxt == "*":
                state = "block"
                out.append("  ")
                i += 2
                continue
            if c == '"':
                state = "string"
            elif c == "`":
                state = "raw"
            elif c == "'":
                state = "rune"
            out.append(c)
            i += 1
            continue
        if state == "line":
            if c == "\n":
                state = ""
                out.append("\n")
            else:
                out.append(" ")
            i += 1
            continue
        if state == "block":
            if c == "*" and nxt == "/":
                state = ""
                out.append("  ")
                i += 2
                continue
            out.append("\n" if c == "\n" else " ")
            i += 1
            continue
        # 字符串系：原样保留，只找结束位
        if c == "\\" and state in ("string", "rune") and nxt:
            out.append(c)
            out.append(nxt)
            i += 2
            continue
        closed = (state == "raw" and c == "`") or (state == "string" and c == '"') \
            or (state == "rune" and c == "'")
        out.append(c)
        if closed:
            state = ""
        i += 1
    return "".join(out)
