#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 B 负例控制：密钥主机绑定（F4）、归档接线（F7）与重启后的列表（F8）挡不挡得住。

为什么单独一批：这两批改的都是**判断**而不是格式——"这把 key 能不能发给这台主机"、
"这次跑完有没有留档"。格式错了编译就拦，判断错了什么都不会发生：界面照常响应、
测试照常绿，损失要等到重启之后才显形。所以每条判断都要能被"改坏"验一次。

跑法（前台，分段；原因见 _harness.py 的 select 注释）：
    python scripts/mutation/batch-b-wiring.py        # 全跑
    python scripts/mutation/batch-b-wiring.py 1-4    # 只跑前四条
"""

from __future__ import annotations

import sys
import os

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: F401  # 本脚本自己也在印中文判定行：Windows 默认按 cp936 写 stdout，`✓` 会直接抛异常
from _harness import run_batch  # noqa: E402

GO_TEST = "go test {pkg} -run {run} -count=1 -timeout 120s"


def t(pkg, run, label=None):
    return (label or f"{pkg} -run {run}", GO_TEST.format(pkg=pkg, run=run))


MUTATIONS = [
    # ---------- 归档接线（F7） ----------
    {
        "name": "webui：删掉任务归档调用（跑完只留内存表）",
        "file": "internal/webui/handlers.go",
        "edits": [(
            """		if err := agent.SaveTaskResult(s.Agent.Cfg.DataDir, result); err != nil {
			fmt.Fprintf(os.Stderr, "[gleam] 界面任务未存档：%v\\n", err)
		}
""", "")],
        "targets": [t("./internal/webui", "TestGoalSubmit_ArchivesForReplay")],
    },
    {
        "name": "webui：归档写成空壳（调了，但没把这一次的结果写进去）",
        "file": "internal/webui/handlers.go",
        "edits": [(
            "agent.SaveTaskResult(s.Agent.Cfg.DataDir, result)",
            "agent.SaveTaskResult(s.Agent.Cfg.DataDir, &types.GoalResult{TaskID: result.TaskID, Status: types.GoalSuccess})")],
        "targets": [t("./internal/webui", "TestGoalSubmit_ArchivesForReplay")],
    },
    # 注：**"归档先于终态广播"这一条刻意不做负例控制。** 把顺序改回"先广播后落盘"，
    # 没有任何断言会响（实测存活）——它挡的是"广播之后进程被杀"，而这在一个进程内
    # 测不出来：能测到的都只是"有没有归档"，那是上面第 1、2 条变异的事。
    # 所以这条只以注释的形式留在 handlers.go 里，别在批次表里放一条永远存活的变异，
    # 那只会让下一个跑批的人以为捕获率出了错。
    {

        "name": "定时任务：删掉 fire 路径的归档",
        "file": "cmd/gleam/main.go",
        "edits": [(
            """	if err := agent.SaveTaskResult(rt.cfg.DataDir, res); err != nil {
		fmt.Fprintf(os.Stderr, "[schedule] 任务未存档：%v\\n", err)
	}
""", "")],
        "targets": [t("./cmd/gleam", "TestFireScheduledJob_Notifies")],
    },
    {
        "name": "定时任务：让通知策略重新管住事件推送（送达=弹窗的老口径）",
        "file": "cmd/gleam/main.go",
        "edits": [(
            """	if sink != nil {
		sink.OnTaskDone(ev)
	}""",
            """	if sink != nil && j.ShouldNotify(ev.Succeeded()) {
		sink.OnTaskDone(ev)
	}""")],
        "targets": [t("./cmd/gleam", "TestNotifyScheduledDone_Wiring")],
    },
    {
        # 归档这一半以前**没有**断言挡住：`TestFireScheduledJob_Notifies` 用的是
        # 「失败 + on_failure」，那次本来就要弹窗，所以把归档挪进策略分支它照样绿
        # （已实测：变异下 exit=0）。现在由 `TestFireScheduledJob_ArchiveIgnoresNotifyPolicy`
        # 用三种"不弹窗"的组合钉住——`notify: never` 说的是别打扰，不是别记下来。
        "name": "定时任务：让通知策略管住归档（never 的任务等于从没跑过）",
        "file": "cmd/gleam/main.go",
        "edits": [(
            """	if err := agent.SaveTaskResult(rt.cfg.DataDir, res); err != nil {
		fmt.Fprintf(os.Stderr, "[schedule] 任务未存档：%v\\n", err)
	}
""",
            """	if j.ShouldNotify(res.Status == types.GoalSuccess) {
		if err := agent.SaveTaskResult(rt.cfg.DataDir, res); err != nil {
			fmt.Fprintf(os.Stderr, "[schedule] 任务未存档：%v\\n", err)
		}
	}
""")],
        "targets": [
            t("./cmd/gleam", "TestFireScheduledJob_Notifies"),
            t("./cmd/gleam", "TestFireScheduledJob_ArchiveIgnoresNotifyPolicy"),
        ],
    },
    {
        "name": "列表回到只读内存表（重启后「全部 0」，档案其实在盘上）",
        "file": "internal/webui/handlers.go",
        "edits": [(
            "	archived, skipped, err := agent.ListTaskResults(s.Agent.Cfg.DataDir, listArchiveLimit)",
            "	archived, skipped, err := []*types.GoalResult(nil), 0, error(nil)")],
        "targets": [t("./internal/webui", "TestGoalList_IncludesArchived")],
    },
    {
        "name": "结果不带安全模式（重启后卡片的「对话/工作/编程」整列空掉）",
        "file": "internal/agent/agent.go",
        "edits": [("	result.Mode = mode\n", "")],
        "targets": [t("./internal/webui", "TestGoalSubmit_ArchivesRunMode")],
    },
    # ---------- 任务名规则（F-2 读盘回落的攻击面） ----------
    {
        "name": "SafeTaskName：允许点和斜杠（净化式放行）",
        "file": "internal/agent/runlog.go",
        "edits": [(
            "		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':",
            "		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '/':")],
        "targets": [
            t("./internal/agent", "TestTaskArchivePath_RejectsUnsafe"),
            t("./internal/webui", "TestGoalGet_RejectsPathTraversal"),
        ],
    },
    # ---------- 密钥按主机绑定（F4） ----------
    {
        "name": "KeyScope：退回按整条 URL 归一（改路径就等于换主机）",
        "file": "internal/llm/providers.go",
        "edits": [("	return strings.ToLower(u.Host)", "	return strings.ToLower(s)")],
        "targets": [
            t("./internal/llm", "TestKeyScope"),
            t("./internal/agent", "TestBindLLMKey_FormAndSwitch"),
        ],
    },
    {
        "name": "LLMKeyFor：不看密钥所属主机（旧 key 直接发给新厂商）",
        "file": "internal/agent/llmkey.go",
        "edits": [(
            "	case cfg != nil && cfg.LLM.APIKey != \"\" && cfg.LLM.APIKeyScope == scope:",
            "	case cfg != nil && cfg.LLM.APIKey != \"\":")],
        "targets": [
            # 一条一个 -run：`'A|B'` 里的竖线在 Windows 上会被 cmd 当管道（骨架预检直接拒绝）。
            t("./internal/agent", "TestLLMKey_InjectedKeyBoundAtStartup"),
            t("./internal/agent", "TestBindLLMKey_FormAndSwitch"),
            t("./internal/agent", "TestLLMKeyFor_ExplicitWins"),
            t("./internal/webui", "TestGoalSubmit_WarningDistinguishesKeyHost"),
        ],
    },
    # ---------- 原子写（F5）：判据是文本约定，直接拿闸门脚本当目标 ----------
    {
        "name": "记忆落盘：原子写退回 os.WriteFile（崩溃留半截 JSON）",
        "file": "internal/harness/memory/store.go",
        "edits": [("_ = atomicfile.Write(path, data, 0o644)", "_ = os.WriteFile(path, data, 0o644)")],
        "targets": [("check-atomic-write", "python scripts/check-atomic-write.py .")],
    },
]

if __name__ == "__main__":
    sys.exit(run_batch("批次 B：密钥主机绑定与归档接线的负例控制", MUTATIONS,
                       timeout=240, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
