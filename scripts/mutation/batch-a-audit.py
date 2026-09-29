#!/usr/bin/env python3
"""批次 A「审计四用法」的变异清单。

对应 AI Coding 工程演变对照与升级清单.md 的 P0-1。这一批新增的东西有一个共同点：
**每一处失效都是静默的**——沿用被当成执行、沿用数没印、运行日志漏了沿用步、
缺记录时顺手跑一遍。它们不会报错，只会让复盘的人得出一个错误但看起来合理的结论。
所以变异验证在这里不是走形式：它回答的是"这些错有没有可能悄悄溜过去"。

运行：python scripts/mutation/batch-a-audit.py
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: F401  # 本脚本自己也在印中文判定行：Windows 默认按 cp936 写 stdout，`✓` 会直接抛异常
from _harness import run_batch  # noqa: E402

# 命令里**不能出现 `|`**。`shell=True` 在 Windows 上走 cmd.exe，而 cmd 不认单引号，
# 于是 `-run 'A|B'` 里的 `|` 被当成管道，命令压根没跑起来——而"命令失败"会被
# 记成"变异被捕获"，于是一整批变异拿到一个假的满分。这是最隐蔽的一种假阳性：
# 报错信息看起来像测试在报错，实际是 shell 在报错。
# `-timeout` 不能省：死等类变异（例如"沿用步骤不放行下游"）会让 go test 一直挂着，
# 而 go test 的默认超时是 10 分钟——于是整段验证卡死在一条变异上，看起来像脚本有问题。
# 加了它，死等会在 90 秒内变成 panic，而那正是这类变异该有的症状。
AGENT = ("agent", "go test ./internal/agent/ -count=1 -timeout 90s")
CMD = ("cmd", "go test ./cmd/gleam/ -count=1 -timeout 90s")
SMOKE = ("smoke", "bash scripts/smoke-replay.sh")

MUTATIONS = [
    # ---------- 沿用语义（执行器） ----------
    {
        "name": "carried_not_marked",
        "file": "internal/agent/executor.go",
        # 不标注沿用：报告里"这次成功 3 步"会被读成"这次跑了 3 步"。
        "edits": [("\t\tprev.Carried = true\n", "\t\tprev.Carried = false\n")],
        "targets": [AGENT],
    },
    {
        "name": "carried_count_not_summed",
        "file": "internal/agent/executor.go",
        # 沿用步数不进汇总：一次恢复看起来像"全部重新跑通了"。
        "edits": [("\t\t\tres.Carried++\n", "\t\t\t_ = res.Carried\n")],
        "targets": [AGENT, CMD, SMOKE],
    },
    {
        "name": "carried_does_not_release_dependents",
        "file": "internal/agent/executor.go",
        # 预置了结果却不为它放行完成信号：依赖它的步骤会一直等下去。
        # 这是"预置晚于 goroutine 启动"这类顺序错误的典型症状——死等，而不是报错。
        "edits": [(
            "\t\tclose(state.finished[i])\n\t\t// 沿用的步骤也要落日志",
            "\t\t// 沿用的步骤也要落日志",
        )],
        "targets": [AGENT],
    },
    {
        "name": "carried_steps_not_recorded",
        "file": "internal/agent/executor.go",
        # 沿用步不进运行日志：一次中途退出的恢复在日志里会"少了前几步"，
        # 读的人分不清是沿用了还是丢了。
        "edits": [("\t\te.record(taskID, state.results[i])\n\t\tcarried++", "\t\tcarried++")],
        "targets": [AGENT],
    },
    {
        "name": "carried_step_also_executed",
        "file": "internal/agent/executor.go",
        # 沿用的步骤又被真跑了一遍——恢复退化成重跑，副作用重做。
        # 两处一起改：否则 close 会被调用两次而 panic，那样"捕获"的理由就不对了
        # （我们要证明的是"工具被重复调用"，不是"程序崩了"）。
        "edits": [
            ("\t\tif _, ok := e.Carried[plan.Steps[i].ID]; ok {\n", "\t\tif false {\n"),
            ("\t\tclose(state.finished[i])\n\t\t// 沿用的步骤也要落日志", "\t\t// 沿用的步骤也要落日志"),
        ],
        "targets": [AGENT, SMOKE],
    },
    {
        "name": "carried_double_close_panics",
        "file": "internal/agent/executor.go",
        # 预置与执行循环都去 close 同一个通道 → panic。
        # 这条测的是"预置必须短路掉执行循环"这个不变量本身。
        "edits": [(
            "\t\t\t// 已预置：不执行，也不再 close（通道在上面已经关过，重复关闭会 panic）\n\t\t\tcontinue\n",
            "",
        )],
        "targets": [AGENT],
    },

    # ---------- 运行日志 ----------
    {
        "name": "runlog_write_is_noop",
        "file": "internal/agent/runlog.go",
        "edits": [("\t_, _ = f.Write(append(b, '\\n'))\n", "\t_ = b\n")],
        "targets": [AGENT],
    },
    {
        "name": "runlog_sanitizes_unsafe_name",
        "file": "internal/agent/runlog.go",
        # "净化后照写"会把 a/b 与 a_b 映射到同一个文件：两次不同的运行写进同一份日志，
        # 读的人会以为看到的是完整的一次运行。宁可不记，也不要记错。
        "edits": [(
            "\t\tcase r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':\n\t\tdefault:\n\t\t\treturn \"\"\n",
            "\t\tcase r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':\n\t\tdefault:\n\t\t\t_ = r\n",
        )],
        "targets": [AGENT],
    },
    {
        "name": "runlog_missing_file_is_error",
        "file": "internal/agent/runlog.go",
        # 日志"正常结束就删"，文件不存在是绝大多数任务的状态。
        # 把它变成错误，等于让每个调用点都得绕开一次预期中的失败。
        "edits": [("\t\tif os.IsNotExist(err) {\n\t\t\treturn nil, nil\n\t\t}\n", "\t\tif false {\n\t\t\treturn nil, nil\n\t\t}\n")],
        "targets": [AGENT],
    },
    {
        "name": "runlog_broken_line_fails_whole",
        "file": "internal/agent/runlog.go",
        # 半行 JSON 是崩溃现场的正常形态。整份拒绝解析等于把仅有的凭据也丢掉。
        "edits": [(
            "\t\tvar r types.StepResult\n\t\tif err := json.Unmarshal([]byte(line), &r); err != nil {\n\t\t\tcontinue\n\t\t}\n",
            "\t\tvar r types.StepResult\n\t\tif err := json.Unmarshal([]byte(line), &r); err != nil {\n\t\t\treturn nil, err\n\t\t}\n",
        )],
        "targets": [AGENT],
    },
    {
        "name": "discard_runlog_noop",
        "file": "internal/agent/runlog.go",
        # 任务记录写成功后不删运行日志：runs/ 无界增长，且"留下的是没跑完的运行"
        # 这个判断不再成立——翻日志的人分不清哪条是半截运行。
        "edits": [("\t_ = os.Remove(path)\n", "\t_ = path\n")],
        "targets": [AGENT, SMOKE],
    },

    # ---------- 计数与渲染（cmd/gleam） ----------
    {
        "name": "countsteps_status_only",
        "file": "cmd/gleam/replay.go",
        # "跑完了但业务上失败"（shell 非零退出…）按 Status 算会被洗成成功——
        # 这是回放报告最不该犯的错，也是两处计数最容易分叉的一项。
        "edits": [(
            "\t\tcase r.Status == types.StepSucceeded && r.Outcome == types.OutcomeFailed:\n\t\t\tfailed++\n",
            "\t\tcase false:\n\t\t\tfailed++\n",
        )],
        "targets": [CMD, SMOKE],
    },
    {
        "name": "countsteps_drops_carried",
        "file": "cmd/gleam/replay.go",
        "edits": [("\t\tif r.Carried {\n\t\t\tcarried++\n\t\t}\n", "\t\tif false {\n\t\t\tcarried++\n\t\t}\n")],
        "targets": [CMD],
    },
    {
        "name": "stepline_hides_carried",
        "file": "cmd/gleam/replay.go",
        # 沿用与复用印成一个词，读的人就无法判断这次有没有产生副作用。
        "edits": [("\t\tline += \" 沿用（未执行）\"\n", "")],
        "targets": [CMD, SMOKE],
    },
    {
        "name": "diff_drops_carried_line",
        "file": "cmd/gleam/replay.go",
        # 不印沿用步数，"这次成功 3 步"会被读成"这次跑了 3 步"。
        "edits": [(
            "\t\tfmt.Fprintf(&out, \"  沿用：  %d 步 → %d 步（沿用=这次没有执行，结果取自上一次）\\n\", a.Carried, b.Carried)\n",
            "",
        )],
        "targets": [SMOKE],
    },
    {
        "name": "carried_warning_removed",
        "file": "cmd/gleam/replay.go",
        # 沿用下来的失败步骤会连累目标步被跳过。不提醒，用户会以为 --from 坏了，
        # 然后改用 --rerun 把副作用又跑一遍。
        #
        # 注意改法必须是**能编译**的：留一句不可达的代码会让整个包编译失败，
        # 于是"被捕获"的理由变成了语法错误，而不是断言真的挡住了行为——
        # 那样的变异什么也证明不了（见 _harness 对无效变异的识别）。
        "edits": [(
            "\treturn fmt.Sprintf(\"提醒：沿用下来的步骤 %s 当时没有成功，依赖它们的步骤这次仍会被跳过——\"+\n"
            "\t\t\"这是当时的前提，不是恢复失败。要改这个前提，请把起点提前到那一步。\", strings.Join(bad, \"、\"))\n",
            "\treturn \"\"\n",
        )],
        "targets": [CMD],
    },

    # ---------- 存档位置与读取入口 ----------
    {
        "name": "save_replay_to_tasks",
        "file": "cmd/gleam/replay.go",
        # 回放落进 tasks/：质量统计（完成率、用户重试率、首次通过率）的分母
        # 就被复盘动作本身污染了。
        "edits": [('\tdir := filepath.Join(dataDir, "replays")\n', '\tdir := filepath.Join(dataDir, "tasks")\n')],
        "targets": [CMD, SMOKE],
    },
    {
        "name": "loadrun_ambiguous_picks_task",
        "file": "cmd/gleam/replay.go",
        # 两个目录同名时随便挑一个：用户以为在看任务，其实看的是某次回放。
        "edits": [("\tif taskOK && replayOK {\n", "\tif false && taskOK && replayOK {\n")],
        "targets": [CMD],
    },
    {
        "name": "resume_missing_result_runs_anyway",
        "file": "cmd/gleam/replay.go",
        # 缺一条历史结果就"顺手跑一遍"：把恢复悄悄变成了重跑，而重跑有副作用。
        # 用户以为只是接着跑，实际上把前面做过的又做了一遍。
        "edits": [(
            "\t\t\tr, ok := src.resultOf(st.ID)\n\t\t\tif !ok {\n",
            "\t\t\tr, ok := src.resultOf(st.ID)\n\t\t\tif false && !ok {\n",
        )],
        "targets": [SMOKE],
    },

    # ---------- 接线 ----------
    {
        "name": "replay_sink_not_wired",
        "file": "cmd/gleam/replay.go",
        # 回放不接运行日志：回放记录是跑完才写的，一次跑到一半就没了的回放
        # 事后什么都查不到。
        "edits": [("\t\te.Sink = agent.NewRunLog(dataDir)\n", "\t\t_ = dataDir\n")],
        "targets": [CMD],
    },
]

if __name__ == "__main__":
    # 用法：python batch-a-audit.py [a-b | 名字子串]
    # 分段跑的原因见 _harness.select：后台执行会被沙箱拦截，而拦截看起来
    # 就像"测试失败"，会把变异误记成"已捕获"。
    spec = sys.argv[1] if len(sys.argv) > 1 else ""
    sys.exit(run_batch("批次 A · 审计四用法 变异验证", MUTATIONS, spec=spec))
