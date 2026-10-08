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

import importlib.util
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: F401  # 本脚本自己也在印中文判定行
from _harness import run_batch  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def owner_version() -> str:
    """版本号从 owner（`internal/buildinfo`）读，**不在这里再抄一份**。

    这一批打的就是"抄字面量"这件事，自己抄一份的话后果很具体：升版本那天，
    注进去的旧号不再等于 owner，闸门照着新号扫前端，什么都扫不到——
    前两条变异会**静默存活**，而批次看起来还是跑过了。（1.0.0 那次真的踩到：
    批次里写死的 `v0.1.0` 一夜之间变成了一条打不中任何东西的变异。）
    """
    path = os.path.join(ROOT, "scripts", "check-version-owner.py")
    spec = importlib.util.spec_from_file_location("check_version_owner", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    version = mod.app_version(ROOT)
    if not version:
        raise SystemExit("读不到 internal/buildinfo 的 Version：批次 H 的判据已失效，先修它")
    return version


VERSION = owner_version()

CHECK = ("version-owner", "python scripts/check-version-owner.py .")

MUTATIONS = [
    {
        "name": "HTML 占位退回写死的版本号（升版本时这一处就会永远停在旧号）",
        "file": "internal/webui/static/index.html",
        "edits": [('id="me-version">—', 'id="me-version">v' + VERSION)],
        "targets": [CHECK],
    },
    {
        "name": "JS 对 /api/info 的取值退回字面量兜底",
        "file": "internal/webui/static/app.js",
        "edits": [("const ver = info.version ? 'v' + info.version : '未知';",
                   "const ver = 'v' + (info.version || '" + VERSION + "');")],
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
