#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""文档字数预算检查。

**只告警，永远 exit 0。**
为什么不做成门禁：长度是「找得快」的代理指标，不是目的；卡死会逼人删掉该留的解释。
见 docs/known-limits.md「字数预算做成硬门禁 → 明确不做」。

单位是**字符数（含空白）**。PDF 原文写的是「1950 词」，那是英文语料的量法；
Gleam 的文档以中文为主，中文 1 字 ≈ 1 token，与「1950 词 ≈ 1950 token」的意图一致，
所以统一按字符数校验（可机械校验，不需要分词器）。

预算：
  根 AGENTS.md                 ≤ 1950
  子树 AGENTS.md（**/AGENTS.md）≤ 750
  docs/*.md 叙述型              ≤ 1320
  docs/*.md 索引型（表格行 ≥ 10）≤ 2640，且每行 ≤ 60
  README.md 前言（首个 "## " 之前）≤ 994（全文不设限：README 是能力目录，不是入口）

索引型为什么放宽：预算管的是**啰嗦**，不是**事实数量**。
一张「不做什么」的汇总表条目数由真实决定的数量决定，压到 1320 就得删掉事实，
那正是 B7 要防的「agent 反复提出同样的建议」。索引型改为**每行 ≤ 60 字符**——
保证一眼能扫完一条，这才是「找得快」。
"""

import io
import os
import re
import sys

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

ROOT_AGENTS = 1950
SUB_AGENTS = 750
DOCS_PROSE = 1320
DOCS_INDEX = 2640
DOCS_ROW = 60
README_PREAMBLE = 994

SKIP_DIRS = {".git", ".workbuddy-ai", "bin", "node_modules", "__pycache__"}
SKIP_FILE_SUFFIX = (".mutbak",)

ROW_RE = re.compile(r"^\|")
SEP_RE = re.compile(r"^\|[\s:\-|]+\|$")
H2_RE = re.compile(r"^## ", re.M)


def read(path):
    with io.open(path, encoding="utf-8", errors="replace") as f:
        return f.read()


def table_rows(text):
    """表格数据行（排除表头分隔行）。"""
    out = []
    for line in text.splitlines():
        if ROW_RE.match(line) and not SEP_RE.match(line):
            out.append(line)
    return out


def walk(root):
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for name in filenames:
            if name.endswith(SKIP_FILE_SUFFIX):
                continue
            yield os.path.join(dirpath, name)


def check_agents(path, text, is_root, out):
    n = len(text)
    budget = ROOT_AGENTS if is_root else SUB_AGENTS
    out.append((path, n, budget, "根入口" if is_root else "子树入口"))


def check_doc(path, text, out):
    rows = table_rows(text)
    index_like = len(rows) >= 10
    if index_like:
        out.append((path, len(text), DOCS_INDEX, "docs·索引型"))
        for row in rows:
            if len(row) > DOCS_ROW:
                out.append(("%s → 超长行" % path, len(row), DOCS_ROW, "docs·单行"))
    else:
        out.append((path, len(text), DOCS_PROSE, "docs·叙述型"))


def check_readme(path, text, out):
    idx = len(text)
    for m in H2_RE.finditer(text):
        idx = m.start()
        break
    out.append((path + " 前言", idx, README_PREAMBLE, "README 前言"))


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    rows = []

    for path in walk(root):
        base = os.path.basename(path)
        rel = os.path.relpath(path, root).replace(os.sep, "/")
        if base == "AGENTS.md":
            check_agents(rel, read(path), rel == "AGENTS.md", rows)
        elif base == "README.md" and rel == "README.md":
            check_readme(rel, read(path), rows)
        elif base.endswith(".md") and rel.startswith("docs/"):
            check_doc(rel, read(path), rows)

    rows.sort(key=lambda r: r[0])
    over = 0
    print("文档字数预算（告警，不阻断构建）")
    for path, n, budget, kind in rows:
        flag = "OK  " if n <= budget else "告警"
        if n > budget:
            over += 1
        print("  %s %-46s %5d / %5d  %s" % (flag, path, n, budget, kind))
    if over:
        print("")
        print("  %d 处超预算。预算管的是啰嗦，不是事实数量：" % over)
        print("  先看能不能把解释挪到它的 home（设计文档 §4.6.x），别先删事实。")
    else:
        print("  全部在预算内。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
