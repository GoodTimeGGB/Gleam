#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 J 负例控制：候补目标那三条口径，静态判据与运行时断言挡不挡得住。

这批要防的漂移有一个共同点：**都不会让界面看起来不对**。
  * 清单登记三条、派生只接两条——界面不会报错，只是那一类永远不出现，
    而 README 和设计文档都写着"从这三条线索里想"。
  * 派生接了却没牌子——界面上那张牌子裸奔成 `half_done`（M3 那条老坑在这层同样成立）。
  * 文案里退回 `**强调**`——不弄坏任何逻辑，只是卡片上多出两个没人要的星号。

第四条最坏：提议层伸了手。三条口径里"只提议不执行"**天生测不到**——它描述的正是
"这里没有发生的事"，没有输出、没有副作用、没有可断言的状态变化，而它在 diff 里
长得像"顺手加个快捷入口"。所以只能靠读源码判（check-cue-owner.py 第 ② 条），
而读源码的判据一旦自己的解析失效就会**变成点头**，因此第九条变异专门打闸门自己。

九条变异（1~5 与 9 只跑 python 闸门，秒级；6~7 要编译 go test，慢在那儿；8 跑 DOM 锚点）：
1. `cueSignals` 少登记一项 → "派生了没登记"要响（这一页说的"只从这几条里想"成假话）。
2. `cueSignals` 多加一项没人派生 → "登记了没派生"要响（清单成许愿单）。
3. `cueSignalText` 删掉一个分支 → "派生了没牌子"要响。
4. `CueView` 里调一次 `RunGoal` → "提议层没有手"要响。**这条变异会真的跑一个任务**，
   所以只把 python 闸门设为目标、不跑 go test：要证的是判据读不读得出来，不是它坏在哪。
   写成 `a.RunGoal(nil, ...)` 而不是引用一个不存在的东西——变异必须**能编译**，
   否则"被捕获"的原因是语法错误，不是判据真的挡住了行为。
5. 卡片理由里退回 `**做法**` → markdown 判据要响。
6. `half_done` 分支还在但不吐行（`_ = halfDoneRows(...)`）→ 闸门照绿（分支确实还在），
   这时只有运行时那条"每条线索到底浮没浮出一张卡"的断言能响。**这条就是"闸门只读源码"
   判不出的那一半**，所以它必须归 Go。
7. 「别再提」按错的指纹记 → `TestCues_DismissSurvivesRestart` 要响。界面看得到"已按下"，
   撤销入口也开着，而那张卡下一屏又回来了——用户的决定没落盘，却没有任何一屏会说这件事。
8. 前端把截断提示的 id 换了个名 → DOM 锚点判据要响。`$('#x')` 拿到 null 不抛错，
   后端算好的"另有 2 条没挤进来"就此静默消失，而配额看起来是满打满算的三条。
9. 把清单改成 `append(...)` 拼出来（一个合法的 Go 写法）→ 闸门的正则读不出 `cueSignals`，
   **必须判红而不是判绿**。这条打的不是产品代码，是判据自己：解析失败却报"通过"的闸门
   比没有闸门更坏，它给出的是"已验证"的错觉。

跑法（前台；后台会被沙箱拦在 Go 构建缓存上，一次被拦下的命令看起来就是"测试失败"，
于是变异被记成"已捕获"——那会给出一个假的满分）：
    python scripts/mutation/batch-j-cue-owner.py
    python scripts/mutation/batch-j-cue-owner.py 1-5
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: E402,F401  # 本脚本自己也在印中文判定行
from _harness import run_batch  # noqa: E402

CUE_CHECK = ("cue-owner", "python scripts/check-cue-owner.py .")
DOM_CHECK = ("dom-anchors", "python scripts/check-dom-anchors.py .")

SIGNALS = 'var cueSignals = []string{"repeat_failure", "half_done", "tool_failure"}'

MUTATIONS = [
    {
        "name": "清单少登记一项：deriveCues 还在派生 half_done，这一页说的「只从这几条里想」成假话",
        "file": "internal/agent/cues.go",
        "edits": [(SIGNALS, 'var cueSignals = []string{"repeat_failure", "tool_failure"}')],
        "targets": [CUE_CHECK],
    },
    {
        "name": "清单多登记一项却没人派生（calendar_sync）：那一类永远不出现，清单成了许愿单",
        "file": "internal/agent/cues.go",
        "edits": [(SIGNALS, SIGNALS.replace('"tool_failure"}', '"tool_failure", "calendar_sync"}'))],
        "targets": [CUE_CHECK],
    },
    {
        "name": "牌子缺一条：half_done 浮得出卡，界面上却裸奔成枚举值（M3 那条老坑）",
        "file": "internal/agent/cues.go",
        "edits": [('case "half_done":\n\t\treturn "同一个目标每次只做一半"\n', "")],
        "targets": [CUE_CHECK],
    },
    {
        "name": "提议层伸了手：CueView 顺手提交了一个任务（这一层的唯一保证就是它没有手）",
        "file": "internal/agent/cues.go",
        "edits": [("\tkept := loadCueStore(a.Cfg.DataDir)",
                   '\ta.RunGoal(nil, types.GoalRequest{Goal: "顺手把这件事做了"})\n'
                   "\tkept := loadCueStore(a.Cfg.DataDir)")],
        "targets": [CUE_CHECK],
    },
    {
        "name": "卡片理由退回 markdown 强调：星号在界面上原样显示（这次是第三次）",
        "file": "internal/agent/cues.go",
        "edits": [("它反复出现说明卡住的是做法，不是运气。",
                   "它反复出现说明卡住的是**做法**，不是运气。")],
        "targets": [CUE_CHECK],
    },
    {
        "name": "派生分支还在但不吐行：闸门照绿，只有运行时断言能响（闸门只读源码的那一半）",
        "file": "internal/agent/cues.go",
        "edits": [('case "half_done":\n\t\t\trows = append(rows, halfDoneRows(groups)...)',
                   'case "half_done":\n\t\t\t_ = halfDoneRows(groups)')],
        "targets": [("agent-signal-rows",
                     "go test ./internal/agent -run TestCues_EverySignalProducesARow -count=1 -timeout 120s")],
    },
    {
        "name": "「别再提」按错的指纹记：处置存下来了，那张卡下一屏又回来",
        "file": "internal/agent/cues.go",
        "edits": [("if !kept.muted(r.ID) {", 'if !kept.muted(r.ID + "|") {')],
        "targets": [("agent-dismiss",
                     "go test ./internal/agent -run TestCues_DismissSurvivesRestart -count=1 -timeout 120s")],
    },
    {
        "name": "前端把截断提示的 id 换了个名：后端算好的「另有 2 条」静默消失",
        "file": "internal/webui/static/app.js",
        "edits": [("$('#cu-tail').textContent = led.truncated || '';",
                   "$('#cu-box').textContent = led.truncated || '';")],
        "targets": [DOM_CHECK],
    },
    {
        "name": "清单换成 append 拼出来（合法写法）：闸门读不出 cueSignals，必须判红而不是判绿",
        "file": "internal/agent/cues.go",
        "edits": [(SIGNALS,
                   'var cueSignals = append([]string{"repeat_failure", "half_done"}, "tool_failure")')],
        "targets": [CUE_CHECK],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 J：候补目标口径的负例控制", MUTATIONS,
                       timeout=300, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
