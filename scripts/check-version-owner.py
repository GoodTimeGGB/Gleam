#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""前端不许再抄一份版本号，也不许替「有没有新版」下结论。

**为什么要有**：「检查更新」这一行以前写的是「当前版本 v0.1.0，已是本地运行的版本」——
两个缺陷叠在一句文案里：
  * `v0.1.0` 是 `internal/buildinfo.Version` 的又一处复制：HTML 里写死一份、JS 兜底再写一份。
    升版本时漏掉任何一处，界面就对外报一个旧号，而它看起来像是从后端来的（AGENTS.md 铁律 1）。
  * 「已是本地运行的版本」是在**声称一个本机不可能知道的结论**：没有联网更新源，本机无从判断
    别处有没有新版。它不是"功能待补"，是把"我不知道"讲成了答案——比留空更坏，
    因为用户会照着这个结论停止行动。

**为什么不靠 code review 拦住**：抄版本号在 diff 里长得和人畜无害的占位文本一模一样；
而假结论是**产品口径**问题，看代码的人不会觉得它错了。两条判据都是机械的，
所以各配一条 exit non-zero 的命令（AGENTS.md 铁律 3）。

**判据跟着 owner 走，不是写死的数字**：字面量取自 `internal/buildinfo/buildinfo.go`，
所以升版本时这条判据自动跟着挪，不需要有人记得来改脚本。

**怎么改才通过**：版本只从 `GET /api/info` 取（`internal/webui/handlers.go` 引用
`buildinfo.Version`）；说不准的事就说不准，别用完成时态替它圆。

**自我失效的防线**：解析不到 `Version`、一个前端文件都没扫到，都直接失败——
宁可红着，也不给"已通过"的错觉。

**负例控制自带**：`--self-test` 现造三份坏文本断言会被判红，再拿三份**长得像**的干扰文本
（`127.0.0.1`、SVG 坐标、别的版本号）断言不许误报——点分数字在静态资源里遍地都是，
只写"匹配 `\d+\.\d+\.\d+`"的判据会在第一天就淹掉自己。

用法：python scripts/check-version-owner.py [--self-test] [仓库根目录]
退出码：0 = 前端没有版本字面量、没有更新结论式文案；1 = 否则。
"""
import io
import os
import re
import sys

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

BUILDINFO = os.path.join("internal", "buildinfo", "buildinfo.go")
STATIC = os.path.join("internal", "webui", "static")
SCAN_SUFFIX = (".js", ".html", ".css")

# 替「有没有新版」下结论的文案。只拦**断言式**说法，不拦「最新版本」这类中性词组。
CLAIM_RE = re.compile(r"已是最新|已经是最|当前已是|无需更新|已是本地")


def read_lines(path):
    with io.open(path, "rb") as f:
        return f.read().decode("utf-8", "replace").splitlines()


def app_version(root):
    """从 owner 那里取版本号字面量，而不是在脚本里再写一份。"""
    try:
        text = "\n".join(read_lines(os.path.join(root, BUILDINFO)))
    except OSError as e:
        print("读不到 %s：%s" % (BUILDINFO.replace("\\", "/"), e))
        return None
    m = re.search(r'^\s*(?:(?:const|var)\s+)?Version\s*=\s*"([^"]*)"', text, re.M)
    if not m or not m.group(1).strip():
        print("%s 里解析不到 Version 常量：判据失效，请先修本脚本。" % BUILDINFO.replace("\\", "/"))
        return None
    return m.group(1).strip()


def version_re(version):
    """前后都不许挨着数字或点：`10.1.0`、`0.1.0.1`、`1.87.34` 都不该命中。"""
    return re.compile(r"(?<![\d.])v?" + re.escape(version) + r"(?![\d.])")


def scan(version, docs):
    """docs = [(标签, [行])] → 命中描述列表。判据只写这一份，真文件与负例共用。"""
    lit = version_re(version)
    hits = []
    for label, lines in docs:
        for i, line in enumerate(lines, 1):
            for pat, why in ((lit, "抄了一份版本号（owner 是 internal/buildinfo）"),
                             (CLAIM_RE, "替「有没有新版」下了结论，而本机没有更新源")):
                m = pat.search(line)
                if m:
                    hits.append("%s:%d  「%s」——%s" % (label, i, m.group(0), why))
    return hits


def report(hits, say, ok_line):
    if hits:
        say("前端不许复制版本号，也不许声称已无新版（版本走 GET /api/info，说不准的就说不准）：")
        for h in hits[:20]:
            say("  " + h)
        if len(hits) > 20:
            say("  …另有 %d 处" % (len(hits) - 20))
        return 1
    say(ok_line)
    return 0


def self_test(version) -> int:
    """坏文本必须拦住、长得像的干扰文本不许误报。"""
    bad_cases = [
        ("HTML 占位写死版本号", ['<span id="me-version">v' + version + "</span>"]),
        ("JS 兜底写死版本号", ["const ver = info.version || '" + version + "';"]),
        ("文案声称已是本地运行的版本", ["await alertModal('当前版本，已是本地运行的版本。');"]),
    ]
    decoys = [
        ("本机地址不是版本号", ['api("GET", "http://127.0.0.1:8795/api/info")']),
        ("SVG 点分坐标不是版本号", ['<path d="M1.87.34 L15.62.68"/>']),
        ("别的版本号不归本判据管", ['<span class="badge--version">v9.9.9</span>']),
    ]
    rc = 0
    for name, lines in bad_cases:
        hits = scan(version, [(name, lines)])
        if not hits:
            print("负例没被拦住：%s —— 判据在永远点头，本脚本要修" % name)
            rc = 1
        else:
            print("  拦住：%s（%s）" % (name, hits[0][:64]))
    for name, lines in decoys:
        hits = scan(version, [(name, lines)])
        if hits:
            print("干扰文本被误报：%s —— %s" % (name, hits[0][:64]))
            rc = 1
        else:
            print("  不误报：%s" % name)
    return rc


def main() -> int:
    argv = [a for a in sys.argv[1:] if a != "--self-test"]
    selftest = "--self-test" in sys.argv[1:]
    root = argv[0] if argv else "."
    version = app_version(root)
    if version is None:
        return 1

    base = os.path.join(root, STATIC)
    docs = []
    for dirpath, dirnames, names in os.walk(base):
        dirnames.sort()
        for n in sorted(names):
            if n.endswith(SCAN_SUFFIX):
                path = os.path.join(dirpath, n)
                label = os.path.relpath(path, root).replace("\\", "/")
                try:
                    docs.append((label, read_lines(path)))
                except OSError as e:
                    print("读不到 %s：%s" % (label, e))
                    return 1

    if not docs:
        print("一个前端文件都没扫到（%s）：检查本身失效，请先修本脚本。" % STATIC.replace("\\", "/"))
        return 1

    rc = report(scan(version, docs), print,
                "  版本口径干净：%d 个前端文件，无 %r 字面量、无更新结论式文案" % (len(docs), version))
    if rc != 0 or not selftest:
        return rc
    return self_test(version)


if __name__ == "__main__":
    sys.exit(main())
