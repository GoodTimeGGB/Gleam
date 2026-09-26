#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 C 负例控制：接口清单闸门（`scripts/check-api-docs.py`）到底挡不挡得住。

这一批测的不是业务判断，而是**闸门本身**。它防的漂移长这样：接口注册了、前端也点得到，
但 README 的端点表没写（或反过来写了一条代码里没有的）。两种都不会让任何测试变红——
`go test` 只看代码，代码是对的。所以这条规则唯一的价值就在于它**真的会失败**，
于是它自己必须被改坏过四次：

1. 清单删掉一行 → 要报「缺」。
2. 清单加一条代码里没有的 → 要报「多」。
3. 清单退回 `… *` 通配写法 → 也要报「缺」。**这条最要紧**：活文档里正是那个写法
   让四个端点消失了两年，而它看着完全合规（"我写了 /api/skills 啊"）。
4. 路由解析不到了（目录换了）→ 要报「检查本身失效」并 exit 1，**不能报通过**。
   一个静默返回 0 的检查器，比没有检查器更坏。

跑法（前台；本批只跑 python，秒级，不像 go test 那样被沙箱卡在构建缓存上）：
    python scripts/mutation/batch-c-inventory.py
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from _harness import run_batch  # noqa: E402

CHECK = ("api-docs", "python scripts/check-api-docs.py .")

MUTATIONS = [
    {
        "name": "清单删掉一行（成长与角色整组不见）",
        "file": "README.md",
        "edits": [(
            "`GET /api/growth` · `GET /api/growth/recent` · `GET /api/roles`"
            " | 成长统计（等级、事件计数）/ 最近事件流 / 可用角色列表 |\n", "")],
        "targets": [CHECK],
    },
    {
        "name": "清单多写一条代码里没有的端点",
        "file": "README.md",
        "edits": [("`GET /api/events` | SSE 事件流",
                   "`GET /api/phantom-route` · `GET /api/events` | SSE 事件流")],
        "targets": [CHECK],
    },
    {
        "name": "技能那行退回通配写法 `/api/skills*`（历史上就是这么漏掉的）",
        "file": "README.md",
        "edits": [(
            "`GET /api/skills` · `POST /api/skills` · `POST /api/skills/{name}/run`"
            " · `POST /api/skills/{name}/enabled` · `DELETE /api/skills/{name}`",
            "`GET/POST /api/skills*`")],
        "targets": [CHECK],
    },
    {
        "name": "路由换了目录，解析不到——必须报「检查本身失效」而不是通过",
        "file": "scripts/check-api-docs.py",
        "edits": [('WEBUI = os.path.join("internal", "webui")',
                   'WEBUI = os.path.join("internal", "llm")')],
        "targets": [CHECK],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 C：接口清单闸门的负例控制", MUTATIONS,
                       timeout=90, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
