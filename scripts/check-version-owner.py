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

**官网（`website/`）是同一件事的另一半，规则刻意相反**：应用内前端有 `GET /api/info`
可问，所以那里不许抄；官网是纯静态页，没有出口可问，只能抄一份——那就把这一份关进
`id="dl-version"` 与 `id="dl-date"` 两个打了标记的元素里，要求它**等于 owner**
（版本的 owner 是 `buildinfo`，发布日期的 owner 是 `pack/` 下最新那份
`SHA256SUMS-<date>.txt`，由 `scripts/package.sh` 打包时写出），并且整站只许出现一次。
标记读不到时报"判据失效"而不是"通过"。

**怎么改才通过**：版本只从 `GET /api/info` 取（`internal/webui/handlers.go` 引用
`buildinfo.Version`）；说不准的事就说不准，别用完成时态替它圆。

**自我失效的防线**：解析不到 `Version`、一个前端文件都没扫到，都直接失败——
宁可红着，也不给"已通过"的错觉。

**负例控制自带**：`--self-test` 现造三份坏文本断言会被判红，再拿三份**长得像**的干扰文本
（`127.0.0.1`、SVG 坐标、别的版本号）断言不许误报——点分数字在静态资源里遍地都是，
只写"匹配 `\d+\.\d+\.\d+`"的判据会在第一天就淹掉自己。官网那六种形状也在里头，
包括"徽标被整个删掉"——那要报红，不能因为读不到就点头。

用法：python scripts/check-version-owner.py [--self-test] [仓库根目录]
退出码：0 = 应用内前端无版本字面量、官网徽标与日期都等于 owner、两边都无更新结论式文案；1 = 否则。
"""
import io
import os
import re
import sys

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

BUILDINFO = os.path.join("internal", "buildinfo", "buildinfo.go")
STATIC = os.path.join("internal", "webui", "static")
WEBSITE = "website"
WEBSITE_PAGE = os.path.join(WEBSITE, "index.html")
PACK = "pack"
SCAN_SUFFIX = (".js", ".html", ".css")
SUMS_RE = re.compile(r"^SHA256SUMS-(\d{8})\.txt$")

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


def scan(version, docs, ban_literal=True):
    """docs = [(标签, [行])] → 命中描述列表。判据只写这一份，真文件与负例共用。

    `ban_literal=False` 只用于官网：静态页没有 `/api/info` 可问，允许抄一份版本号，
    改由 `site_hits` 断言"抄的那一份等于 owner、且只有一处"。
    """
    lit = version_re(version)
    pats = ((lit, "抄了一份版本号（owner 是 internal/buildinfo）"),
            (CLAIM_RE, "替「有没有新版」下了结论，而本机没有更新源"))
    if not ban_literal:
        pats = pats[1:]
    hits = []
    for label, lines in docs:
        for i, line in enumerate(lines, 1):
            for pat, why in pats:
                m = pat.search(line)
                if m:
                    hits.append("%s:%d  「%s」——%s" % (label, i, m.group(0), why))
    return hits


def report(hits, say, ok_line):
    if hits:
        say("版本口径漂了（应用内前端不许抄版本号；官网抄的那一份必须等于 owner；两边都不许声称已无新版）：")
        for h in hits[:20]:
            say("  " + h)
        if len(hits) > 20:
            say("  …另有 %d 处" % (len(hits) - 20))
        return 1
    say(ok_line)
    return 0


def marked(text, marker):
    """取 id="marker" 那个元素的文本；标记不在就返回 None（＝判据失效，不是＝通过）。"""
    m = re.search(r'id="%s"[^>]*>\s*([^<]*?)\s*<' % re.escape(marker), text)
    return m.group(1).strip() if m else None


def release_date(root):
    """「这次发布是哪天」的 owner：`pack/` 下最新那份 `SHA256SUMS-<date>.txt`。

    它由 `scripts/package.sh` 在打包时写出，所以官网上那个日期不需要有人记得改——
    漏改就是这道判据报红。
    """
    try:
        names = os.listdir(os.path.join(root, PACK))
    except OSError as e:
        print("读不到 %s/：%s" % (PACK, e))
        return None
    dates = sorted(m.group(1) for m in (SUMS_RE.match(n) for n in names) if m)
    if not dates:
        print("%s/ 下没有 SHA256SUMS-<date>.txt：判据失效，请先修本脚本。" % PACK)
        return None
    d = dates[-1]
    return "%s-%s-%s" % (d[:4], d[4:6], d[6:])


def site_hits(page_text, scan_texts, version, latest_date):
    """官网判据。**它和应用内前端的规则相反**，理由要写在这儿：

    应用内前端有 `GET /api/info` 可问，所以那里**不许**抄版本号；官网是纯静态页，
    没有后端可问，只能抄一份——那就把"抄"关进一个打了标记的元素里，并要求它
    **等于 owner**。两种做法都跟着 owner 走，区别只在有没有出口可问。

    返回 None ＝ 标记读不到（判据本身失效，必须报红，不许当成通过）。
    """
    hits = []
    for marker, want, why in (
        ("dl-version", "v" + version, "版本徽标要等于 owner（internal/buildinfo）"),
        ("dl-date", latest_date, "发布日期要等于 pack/ 下最新那份校验和的日期"),
    ):
        got = marked(page_text, marker)
        if got is None:
            print("%s 里找不到 id=\"%s\"：判据失效（访客看不出这是哪个版本、哪天发的），"
                  "先修页面或本脚本。" % (WEBSITE_PAGE.replace("\\", "/"), marker))
            return None
        if got != want:
            hits.append("%s  id=\"%s\" 写着「%s」，应为「%s」——%s"
                        % (WEBSITE_PAGE.replace("\\", "/"), marker, got, want, why))
    lit = version_re(version)
    n = sum(len(lit.findall(line)) for _, lines in scan_texts for line in lines)
    if n > 1:
        hits.append("官网里 v%s 出现 %d 次，只许出现在 id=\"dl-version\" 那一处"
                    "——多一处就多一份会漂移的副本" % (version, n))
    hits.extend(scan(version, scan_texts, ban_literal=False))
    return hits


def website_docs(root):
    """官网顶层的 html/js/css（**不进 website/dist/**，那里是二进制产物）。"""
    base = os.path.join(root, WEBSITE)
    docs = []
    try:
        names = sorted(os.listdir(base))
    except OSError as e:
        print("读不到 %s/：%s" % (WEBSITE, e))
        return None
    for n in names:
        path = os.path.join(base, n)
        if not n.endswith(SCAN_SUFFIX) or not os.path.isfile(path):
            continue
        label = os.path.join(WEBSITE, n).replace("\\", "/")
        try:
            docs.append((label, read_lines(path)))
        except OSError as e:
            print("读不到 %s：%s" % (label, e))
            return None
    return docs or None


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

    day = "2026-01-02"
    good_page = ('<p class="dl-meta"><span class="dl-ver" id="dl-version">v' + version + "</span>"
                 '<time id="dl-date" datetime="' + day + '">' + day + "</time></p>")
    site_cases = [
        ("官网徽标与 owner 一致（不许误报）", good_page, False),
        ("官网徽标停在旧号", good_page.replace("v" + version, "v0.0.1"), True),
        ("官网发布日期没跟着最新那次打包",
         good_page.replace(day + "</time>", "2000-01-01</time>"), True),
        ("官网把版本号抄了两处", good_page + "<p>本版 v" + version + " 修了三处。</p>", True),
        ("官网声称已是最新", good_page + "<p>已是最新，无需更新。</p>", True),
        ("官网少了版本徽标（判据失效应报红，不是报通过）", "<p>下载</p>", True),
    ]
    for name, page, want_bad in site_cases:
        hits = site_hits(page, [(WEBSITE_PAGE, page.splitlines())], version, day)
        bad = hits is None or bool(hits)
        if bad != want_bad:
            print("官网负例判错了：%s —— 期望%s，实际%s"
                  % (name, "报红" if want_bad else "通过", "报红" if bad else "通过"))
            rc = 1
        else:
            print("  %s：%s" % ("拦住" if want_bad else "不误报", name))
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

    site_docs = website_docs(root)
    latest = release_date(root)
    if site_docs is None or latest is None:
        rc = 1
    else:
        try:
            page = "\n".join(read_lines(os.path.join(root, WEBSITE_PAGE)))
        except OSError as e:
            print("读不到 %s：%s" % (WEBSITE_PAGE, e))
            return 1
        site = site_hits(page, site_docs, version, latest)
        if site is None:
            rc = 1
        else:
            rc = report(site, print,
                        "  官网口径跟着 owner：%d 个静态文件，徽标 v%s、发布日期 %s"
                        % (len(site_docs), version, latest)) or rc

    if rc != 0 or not selftest:
        return rc
    return self_test(version)


if __name__ == "__main__":
    sys.exit(main())
