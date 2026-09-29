#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 E 负例控制：现场栏的读数（F11）与审批的寿命（F11-f）挡不挡得住。

为什么单独一批：这一批全是**"数字与状态对不对得起用户"**那类判断——
"这一格的条数从哪来"、"任务停了审批还算不算等人"。它们改错了不会编译失败、
不会让界面打不开，只会让界面**安静地说谎**：数字停在旧值上、卡片上多一个
永远等不到的按钮。所以每条都要能被"改坏"验一次。

五条变异，外加一条刻意不做：

1. 取消时不摘审批 → 任务永远停在 running，审批名单还说"有一件事等你决定"。
2. 摘除只删登记、不送裁决 → 看着处理了，引擎那一行还等在门后（这条最阴：
   代码里有 drop，行为上等于没 drop）。
3. 取消一律写成"用户拒绝了执行计划" → 用户按的是停止，卡片上说他自己拒的。
4. /api/info 不带 memory → 现场栏「记忆条目」永远是 0，而记忆确实在盘上。
5. 门面 MemoryCount 恒 0 → 同一个症状的另一处源头（判据要能分别指出来）。

至于"审批数量该由谁来数"（前端曾经同时信 DOM 类名与接口种子两个 owner）：那条
判断住在 JS 里，套不进"改坏 Go → 编译 → 跑测试"的骨架，所以**不在本批放一条
永远存活的变异**，由 `scripts/check-dom-anchors.py` 与人工走查看着。

跑法（前台，可分段；原因见 _harness.py 的 select 注释）：
    python scripts/mutation/batch-e-rail-lifecycle.py        # 全跑
    python scripts/mutation/batch-e-rail-lifecycle.py 1-3    # 只跑前三条
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: F401  # 本脚本自己也在印中文判定行：Windows 默认按 cp936 写 stdout，`✓` 会直接抛异常
from _harness import run_batch  # noqa: E402

GO_TEST = "go test {pkg} -run {run} -count=1 -timeout 120s"
WEBUI = "./internal/webui"


def t(pkg, run, label=None):
    return (label or f"{pkg} -run {run}", GO_TEST.format(pkg=pkg, run=run))


MUTATIONS = [
    # ---------- 审批的寿命 ----------
    {
        "name": "取消时不摘审批（任务永远停在审批闸门上）",
        "file": "internal/webui/handlers.go",
        "edits": [(
            """	_ = s.Agent.CancelTask(id)
	// 取消之后必须叫醒停在审批上的那一轮：ctx 已经取消，但引擎的整计划闸门等的是
	// 审批通道，不是 ctx。"取消返回 200、任务还在 running"就是这么来的。
	s.dropTaskApprovals(id, "任务已取消")
""",
            "	_ = s.Agent.CancelTask(id)\n")],
        "targets": [t(WEBUI, "TestCancel_PlanFirstGate"), t(WEBUI, "TestCancel_StepGate")],
    },
    {
        # 这一条是上一位的孪生：删掉登记、不送裁决，`/api/approvals` 立刻就说真话了，
        # 于是第 1 条变异看起来"已修复"。只有"任务还等在门后"这半会红。
        "name": "摘除只删登记、不送裁决（引擎那行还阻塞着）",
        "file": "internal/webui/webui.go",
        "edits": [(
            """	for _, w := range stale {
		resp := types.ApprovalResponse{Approved: false, Note: note}
		// 有缓冲，取不到也只可能是已被裁决：那种情况下不该再改它的答案。
		select {
		case w.ch <- resp:
		default:
		}
	}
""", "")],
        "targets": [t(WEBUI, "TestCancel_PlanFirstGate")],
    },
    {
        "name": "取消的原因一律写成「用户拒绝了执行计划」",
        "file": "internal/agent/agent.go",
        "edits": [(
            """				cause := "用户拒绝了执行计划"
				switch {
				case ctx.Err() != nil:
					cause = "任务已取消"
				case resp.Note != "":
					cause = resp.Note
				}
				res := &types.GoalResult{Goal: goal, Status: types.GoalCancelled, Error: cause, Score: 0}
""",
            """				res := &types.GoalResult{Goal: goal, Status: types.GoalCancelled, Error: "用户拒绝了执行计划", Score: 0}
""")],
        "targets": [t(WEBUI, "TestCancel_PlanFirstGate")],
    },
    # ---------- 现场栏的读数（F11） ----------
    {
        "name": "/api/info 不带 memory（现场栏那一格永远显示 0）",
        "file": "internal/webui/handlers.go",
        "edits": [('		"memory": s.Agent.MemoryCount(),', '		"memory": 0,')],
        "targets": [t(WEBUI, "TestInfo_CarriesMemoryCount")],
    },
    {
        "name": "门面 MemoryCount 恒 0（同一个症状的另一处源头）",
        "file": "internal/agent/webfacade.go",
        "edits": [("	return a.Mem.Long.Count()", "	return 0")],
        "targets": [t(WEBUI, "TestInfo_CarriesMemoryCount")],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 E：现场栏读数与审批寿命的负例控制", MUTATIONS,
                       timeout=180, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
