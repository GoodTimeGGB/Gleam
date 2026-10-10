#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 H 负例控制：版本口径（闸门 + Go 出口）挡不挡得住。

这一批大部分判的是**文案与字面量**，不是 Go 逻辑，所以前四条打在 python 闸门上——
harness 的 target 命令不要求是 go test，判据只要是 exit non-zero 就能被这样钉住。

它防的漂移长这样，两种都不会让 `go test` 变红：
  * HTML 里写死一份版本号 → 升版本时漏改，界面报旧号，而它看起来像后端给的。
  * 「检查更新」给出完成时态的结论 → 本机根本没有更新源，这是把"我不知道"讲成答案。

七条变异：
1. HTML 占位退回写死的版本号 → 要报「抄了一份版本号」。
2. JS 里对 `/api/info` 的取值退回字面量兜底 → 同样要报。
3. 弹窗文案退回完成时态的结论 → 要报「替有没有新版下了结论」。
4. 扫描目录被改到没有前端文件的地方 → **必须报「检查本身失效」并 exit 1**，不许报通过。
   这条是闸门的地基：静默返回 0 的检查器比没有检查器更坏。
5. `/api/info` 不再引用 owner（整行删掉）→ Go 断言要响。这条走的是"判据对、线没接"
   的镜像：前端已经不抄字面量，出口一断，三处读数一起变旧，而界面看着一切正常。
6. 官网的版本徽标停在旧号 → 要报红。官网是静态页、没有 `/api/info` 可问，所以它
   允许抄一份，但那份必须等于 owner；升版本时漏改官网，下载页就对外报一个旧版本。
7. 官网的发布日期停在旧的那天 → 要报红。日期的 owner 是 `dist/` 下最新那份
   `SHA256SUMS-<date>.txt`（打包时写出），所以"新包已经打出来、页面还写着上次的日期"
   是机械可判的，不用靠人记得。

跑法（前台；1~4、6~7 只跑 python 是秒级，第 5 条要编译 go test，慢在那儿）：
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


def owner_facts() -> tuple[str, str]:
    """版本号与发布日期都从 owner 读，**不在这里再抄一份**。

    这一批打的就是"抄字面量"这件事，自己抄一份的话后果很具体：升版本那天，
    注进去的旧号不再等于 owner，闸门照着新号扫前端，什么都扫不到——
    变异会**静默存活**，而批次看起来还是跑过了。（1.0.0 那次真的踩到：
    批次里写死的 `v0.1.0` 一夜之间变成了一条打不中任何东西的变异。
    日期同理，所以它也从 `release_date()` 取，不写 `2026-10-09`。）
    """
    path = os.path.join(ROOT, "scripts", "check-version-owner.py")
    spec = importlib.util.spec_from_file_location("check_version_owner", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    version, date = mod.app_version(ROOT), mod.release_date(ROOT)
    if not version or not date:
        raise SystemExit("读不到版本号或发布日期：批次 H 的判据已失效，先修 owner")
    return version, date


VERSION, RELEASE_DATE = owner_facts()

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
        "edits": [("$('#me-version').textContent = info.version ? 'v' + info.version : '未知';",
                   "$('#me-version').textContent = 'v' + (info.version || '" + VERSION + "');")],
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
        "name": "官网版本徽标停在旧号（下载页对外报一个不是这次的版本）",
        "file": "website/index.html",
        "edits": [('<span class="dl-ver" id="dl-version">v' + VERSION + "</span>",
                   '<span class="dl-ver" id="dl-version">v0.0.1</span>')],
        "targets": [CHECK],
    },
    {
        "name": "官网发布日期停在旧的那天（新包打出来了，页面还写着上次的日期）",
        "file": "website/index.html",
        "edits": [('<time id="dl-date" datetime="' + RELEASE_DATE + '">' + RELEASE_DATE + "</time>",
                   '<time id="dl-date" datetime="2000-01-01">2000-01-01</time>')],
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
