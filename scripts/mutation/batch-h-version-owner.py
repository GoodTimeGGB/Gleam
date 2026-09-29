#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 H 负例控制：版本口径（闸门 + Go 出口）挡不挡得住。

这一批大部分判的是**文案与字面量**，不是 Go 逻辑，所以前四条打在 python 闸门上——
harness 的 target 命令不要求是 go test，判据只要是 exit non-zero 就能被这样钉住。

它防的漂移长这样，两种都不会让 `go test` 变红：
  * HTML 里写死一份版本号 → 升版本时漏改，界面报旧号，而它看起来像后端给的。
  * 「检查更新」给出完成时态的结论 → 本机根本没有更新源，这是把"我不知道"讲成答案。

五条变异：
1. HTML 占位退回写死的版本号 → 要报「抄了一份版本号」。
2. JS 里对 `/api/info` 的取值退回字面量兜底 → 同样要报。
3. 弹窗文案退回完成时态的结论 → 要报「替有没有新版下了结论」。
4. 扫描目录被改到没有前端文件的地方 → **必须报「检查本身失效」并 exit 1**，不许报通过。
   这条是闸门的地基：静默返回 0 的检查器比没有检查器更坏。
5. `/api/info` 不再引用 owner（整行删掉）→ Go 断言要响。这条走的是"判据对、线没接"
   的镜像：前端已经不抄字面量，出口一断，三处读数一起变旧，而界面看着一切正常。

跑法（前台；1~4 只跑 python 是秒级，第 5 条要编译 go test，慢在那儿）：
    python scripts/mutation/batch-h-version-owner.py
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: F401  # 本脚本自己也在印中文判定行
from _harness import run_batch  # noqa: E402

CHECK = ("version-owner", "python scripts/check-version-owner.py .")

MUTATIONS = [
    {
        "name": "HTML 占位退回写死的版本号（升版本时这一处就会永远停在旧号）",
        "file": "internal/webui/static/index.html",
        "edits": [('id="me-version">—', 'id="me-version">v0.1.0')],
        "targets": [CHECK],
    },
    {
        "name": "JS 对 /api/info 的取值退回字面量兜底",
        "file": "internal/webui/static/app.js",
        "edits": [("const ver = info.version ? 'v' + info.version : '未知';",
                   "const ver = 'v' + (info.version || '0.1.0');")],
        "targets": [CHECK],
    },
    {
        "name": "弹窗文案退回完成时态的结论（本机没有更新源，这话问不出来）",
        "file": "internal/webui/static/app.js",
        "edits": [("'说不出有没有新版。要升级就替换程序本身——对话、技能、密钥都存在「本地数据」那个目录里，换程序不影响它们。'",
                    "'当前版本已是本地运行的版本，无需更新。'")],
        "targets": [CHECK],
    },
    {
        "name": "扫描目录被改走，一个前端文件都扫不到——必须报「检查本身失效」而不是通过",
        "file": "scripts/check-version-owner.py",
        "edits": [('STATIC = os.path.join("internal", "webui", "static")',
                   'STATIC = os.path.join("internal", "llm")')],
        "targets": [CHECK],
    },
    {
        "name": "/api/info 不再引用 owner：前端摘了字面量，出口一断三处读数一起变旧",
        "file": "internal/webui/handlers.go",
        # 连 import 一起删：只删那一行会留下"imported and not used"，编译失败的"捕获"是假的。
        "edits": [('\t"gleam/internal/buildinfo"\n', ""),
                  ('\t\t"version": buildinfo.Version,\n', "")],
        "targets": [("webui-info",
                     "go test ./internal/webui -run TestInfo -count=1 -timeout 120s")],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 H：版本口径的负例控制", MUTATIONS,
                       timeout=300, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
