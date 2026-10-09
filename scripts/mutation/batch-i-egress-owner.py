#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 I 负例控制：出网落点与「连接与出网」台账，双向对账挡不挡得住。

这批要防的漂移有两种，共同点是**都不会让 `go test` 变红，也不会让界面看起来不对**：
  * 新接一条往外发的路径，忘了在台账清单 `egressKinds` 登记。于是这张表承诺的"每一条
    出网路径都有一行"当场变成假话，而缺的那一行不是"少显示一行"——是**连了没交代**。
  * 反过来，清单里登记了一项代码里没人记的出网。那一行永远显示"没有记录"，
    用户读到的是"这台机器没往外面发过东西"，事实是"这台机器压根没往这一类记"。

两种形状在 diff 里都长得和人畜无害的重构一模一样，所以各配一条 exit non-zero 的命令。
第三种是文案里的 markdown 强调：它不弄坏任何逻辑，只是让界面上出现两个没人要的星号——
而"排版有点怪"是走查最容易放过去的东西，所以也给它一条判据（那张表走 textContent）。

七条变异（1~4 只跑 python 闸门，秒级；5~6 要编译 go test，慢在那儿；7 跑 DOM 锚点）：
1. `egressKinds` 少登记一项 → 正向判据（记了却没登记）要响。
2. 删掉一个真落点的接线 → 反向判据（登记了却没人记）要响。
3. 新增一个未登记的落点 → 正向判据要响。写成 `_ = func(...){...}` 而不是引用一个不存在的
   变量：变异必须**能编译**，否则"被捕获"的原因是语法错误，不是判据真的挡住了行为。
4. 文案里退回 `**强调**` → 纯文本判据要响。这条不是洁癖：那张表走 `textContent`，
   星号在真机上原样显示在格子里，而它在源码 diff 里只是"多了两个符号"。
5. `ConnectionView` 少画一行 → 运行时那条"这一行今天到底画没画出来"的断言要响。
   闸门只能读源码，读不出运行态，所以这一层归 Go。
6. 审计没配置却仍然指路 `pending --audit` → 诚实性断言要响（§4.6.33 那一类错：
   把"我不知道"讲成答案，而这里更坏——它把人引向一个不存在的文件，
   他查到的"没有记录"会被读成"确实没出过网"）。
7. 前端把台账表体的 id 换了个名 → DOM 锚点判据要响。`$('#x')` 拿到 null 不抛错，
   整块表体就此静默是空的，而数据在后端算得好好的。

跑法（前台；后台会被沙箱拦在 Go 构建缓存上，一次被拦下的命令看起来就是"测试失败"，
于是变异被记成"已捕获"——那会给出一个假的满分）：
    python scripts/mutation/batch-i-egress-owner.py
    python scripts/mutation/batch-i-egress-owner.py 1-3
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: E402,F401  # 本脚本自己也在印中文判定行
from _harness import run_batch  # noqa: E402

EGRESS_CHECK = ("egress-owner", "python scripts/check-egress-owner.py .")
DOM_CHECK = ("dom-anchors", "python scripts/check-dom-anchors.py .")

MUTATIONS = [
    {
        "name": "台账清单少登记一项：出网照记，界面上却没有这一行（连了没交代）",
        "file": "internal/agent/connections.go",
        "edits": [('var egressKinds = []string{"llm", "web.fetch", "cloud", "feedback", "update", "go.toolchain"}',
                   'var egressKinds = []string{"llm", "web.fetch", "cloud", "go.toolchain"}')],
        "targets": [EGRESS_CHECK],
    },
    {
        "name": "摘掉一个真落点的接线：清单还写着 web.fetch，代码里已经没人记它",
        "file": "cmd/gleam/main.go",
        "edits": [('\twebTool.OnEgress = func(host string, nbytes int) { gate.RecordEgress("web.fetch", host, nbytes) }\n',
                   "")],
        "targets": [EGRESS_CHECK],
    },
    {
        "name": "新接一条出网路径却没登记（telemetry）：这张表说的「全部」当场变假话",
        "file": "cmd/gleam/main.go",
        "edits": [('\tllm.SetEgressHook(func(host string, nbytes int) { gate.RecordEgress("llm", host, nbytes) })\n',
                   '\tllm.SetEgressHook(func(host string, nbytes int) { gate.RecordEgress("llm", host, nbytes) })\n'
                   '\t_ = func(host string, nbytes int) { gate.RecordEgress("telemetry", host, nbytes) }\n')],
        "targets": [EGRESS_CHECK],
    },
    {
        "name": "台账文案退回 markdown 强调：星号在界面上原样显示（真机上发生过）",
        "file": "internal/agent/connections.go",
        "edits": [('Trigger: "模型判断需要看某个网页时——它是只读工具，默认不经过你的批准",',
                   'Trigger: "模型判断需要看某个网页时。它是只读工具，**默认不经过你的批准**",')],
        "targets": [EGRESS_CHECK],
    },
    {
        "name": "ConnectionView 不画云端那一行：清单还在，运行时却少一行",
        "file": "internal/agent/connections.go",
        "edits": [("rows = append(rows, a.cloudRow(rep), a.feedbackRow(rep), updateRow(rep), goToolchainRow(rep), marketRow())",
                   "rows = append(rows, a.feedbackRow(rep), updateRow(rep), goToolchainRow(rep), marketRow())")],
        "targets": [("agent-rows",
                     "go test ./internal/agent -run TestConnections_EveryEgressKindHasARow -count=1 -timeout 120s")],
    },
    {
        "name": "没开审计落盘却仍然指路 pending --audit：把人引向一个不存在的文件",
        "file": "internal/agent/connections.go",
        # 只换串不改分支：改分支会留下 auditPath 未使用之外的怪形状，且判据要试的是
        # "指路那句话到底跟不跟本机配置走"，那就得让配置分支还在、只是话说错。
        "edits": [('back = "这个构建没开审计落盘，内存环之外的事没有记录"',
                   'back = "更早的记录在落盘审计里，`gleam pending --audit --egress` 可只看这一类"')],
        "targets": [("agent-scope",
                     "go test ./internal/agent -run TestConnections_EgressScopeTextFollowsScanWindow -count=1 -timeout 120s")],
    },
    {
        "name": "前端把台账表体的 id 换了个名：后端算得好好的，界面那块永远空白",
        "file": "internal/webui/static/app.js",
        "edits": [("const box = $('#cx-conn-list');",
                   "const box = $('#cx-conn-box');")],
        "targets": [DOM_CHECK],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 I：出网台账的负例控制", MUTATIONS,
                       timeout=300, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
