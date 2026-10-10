#!/usr/bin/env python3
"""批次 K「派发前重算裁决」的变异清单。

这一批盯的失效全是**静默**的：门控、审核模型、审计台账三处都会在参数还是
`$ref:` 占位符时就下结论，然后一切照常跑完——没有报错，只有一句读起来像查过了的话。

- 门控对**非绝对路径**的口径是"按工作区内放过"（`safety.pathTrusted`），而
  `$ref:s1.output.path` 正是一个非绝对路径字符串。真值可能指向信任范围外。
- 审计于是写下"操作路径均在信任路径内"——它在替一次没做过的检查作证。
- 审核模型快筛看到的也是占位符：花钱筛一个假值，真值一次都没被筛过。

**有一条这里测不了**：把审计留痕的理由从"重算后的那次"换回"占位符那次的"
（`Reason: final.Reason` → `Reason: dec.Reason`）。两次裁决都自动放行时，
门控给出的理由字面相同（都是"操作路径均在信任路径内"），没有断言能分辨。
接线本身由下面第 1、2 条钉住：重算不发生、或重算了不问人，都会立刻响。

运行：python scripts/mutation/batch-k-dispatch-verdict.py
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: F401  # 本脚本自己也在印中文判定行：Windows 默认按 cp936 写 stdout，`✓` 会直接抛异常
from _harness import run_batch  # noqa: E402

# 命令里不能出现 `|`，`-timeout` 不能省：理由见 batch-a-audit.py 开头与 README「四种假满分」。
AGENT = ("agent", "go test ./internal/agent/ -count=1 -timeout 90s")

MUTATIONS = [
    {
        "name": "verdict_not_recomputed",
        "file": "internal/agent/executor.go",
        # 不重算：裁决永远停在占位符上。真值出界的那一步会被自动放行并真的执行，
        # 而审计里写着"路径均在信任路径内"。
        "edits": [("\tif hasRefs(step.Args) {\n", "\tif false && hasRefs(step.Args) {\n")],
        "targets": [AGENT],
    },
    {
        "name": "recomputed_but_not_enforced",
        "file": "internal/agent/executor.go",
        # 重算了却不用结果：新裁决说"要问人"，代码当作没看见。
        # 与上一条症状相同、成因不同——分开测才知道是哪一段接线断了。
        "edits": [(
            "\t\tif final.NeedApproval && !dec.NeedApproval {\n",
            "\t\tif false && final.NeedApproval && !dec.NeedApproval {\n",
        )],
        "targets": [AGENT],
    },
    {
        "name": "human_approved_also_recorded_as_auto",
        "file": "internal/agent/executor.go",
        # 去掉"先前没问过人"这个前提：人明明批过，台账却又多出一条"自动放行"。
        # 反方向的假话——把有人看过的动作记成没人看过，复盘时会低估审批量。
        "edits": [(
            "\tif !dec.NeedApproval && !final.NeedApproval && final.Risk != \"low\" && e.Gate != nil {\n",
            "\tif !final.NeedApproval && final.Risk != \"low\" && e.Gate != nil {\n",
        )],
        "targets": [AGENT],
    },
    {
        "name": "placeholder_reviewed_too",
        "file": "internal/agent/executor.go",
        # 第一次裁决也送去快筛：带引用的步骤要付两次审核模型的钱，
        # 而第一次筛的是占位符。不报错、不变慢到可见，只是账单翻倍。
        "edits": [(
            "\tdec := e.adjudicate(ctx, tool, step.Args, preApproved, !hasRefs(step.Args))\n",
            "\tdec := e.adjudicate(ctx, tool, step.Args, preApproved, true)\n",
        )],
        "targets": [AGENT],
    },
    {
        "name": "real_args_never_reviewed",
        "file": "internal/agent/executor.go",
        # 重算那次不快筛：带引用的写操作**一次都没被审核模型看过**，
        # 而界面上"执行前用辅助模型快筛"这个开关看起来是开着的。
        "edits": [(
            "\tfinal = e.adjudicate(ctx, tool, args, preApproved, true)\n",
            "\tfinal = e.adjudicate(ctx, tool, args, preApproved, false)\n",
        )],
        "targets": [AGENT],
    },
]

if __name__ == "__main__":
    # 用法：python batch-k-dispatch-verdict.py [a-b | 名字子串]
    spec = sys.argv[1] if len(sys.argv) > 1 else ""
    sys.exit(run_batch("批次 K · 派发前重算裁决 变异验证", MUTATIONS, spec=spec))
