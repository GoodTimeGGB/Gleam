#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 G 负例控制：改动清单与写前还原（F14）挡不挡得住。

为什么单独一批：这一批改的不是"看得见的界面"，是**承诺**——清单上那句"可还原"。
一句报错的承诺比没有承诺更坏：用户会真的按它去点，而点下去动的是他自己的文件。
所以每条变异都对着一个具体后果写，注释里那句"为什么这处失效是静默的"是本批的门槛。

十二条变异：

1. 清单只按产物核对列 → 失败的写入（`O_TRUNC` 已经截断原文件）不进清单 = **漏报**。
2. 同一路径取**最后**一条快照 → 还原只退到中间状态，用户以为退到了任务开始前。
3. 一次移动不 pairing → 只退目标端 = 把文件复制成两份。
4. 去掉"路径必须在本次清单里"这道闸 → 清单外任意文件都能被"还原"（diff 侧先红）。
5. 还原后不更新归档 → 重启后那一格回到"未还原"，同一个任务报出两份清单。
6. 还原后不把归档回填内存 → 刷新一次详情，清单立刻退回还原前的样子。
7. `reversible()` 恒真 → 内容没留住的路径也报"可还原"，点下去什么也退不回来。
8. 还原目录改成递归删 → 非空目录被连带清掉，那不是"还原"是"顺手清盘"。
9. `DataDir` 没传进执行器 → 快照整层不生效，而清单仍会照常列出（判据对、线没接）。
10. 切行不去 `\\r` → Windows 的 CRLF 文件每次对比都是"全删全加"。
11. 对比忘了加回公共前缀的偏移 → 行号指向文件头，用户按号找不到改动。
12. 无改动时返回空切片而不是 nil → "没动文件"与"动了但列不出来"被压成同一句话。

**本批刻意不放的一条**：把 `changeTarget` 里 `toolutil.ResolveInRoots` 那道 workspace
边界去掉，没有任何断言会响。原因是它与第 4 条互为兜底——快照清单本来就只可能记着
workspace 内的路径，越界路径先被清单守卫挡下。这一道的意义是**纵深**（清单被手工
塞进脏数据时它还在），不是当前可测的判据。不为凑捕获率硬造一条永远捕获的变异。

跑法（前台，可分段；原因见 _harness.py 的 select 注释）：
    python scripts/mutation/batch-g-changes.py         # 全跑
    python scripts/mutation/batch-g-changes.py 1-4     # 只跑前四条
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: F401  # 本脚本自己也在印中文判定行：Windows 默认按 cp936 写 stdout，`✓` 会直接抛异常
from _harness import run_batch  # noqa: E402

GO_TEST = "go test {pkg} -run {run} -count=1 -timeout 180s"
AGENT = "./internal/agent"
WEBUI = "./internal/webui"


def t(pkg, run, label=None):
    return (label or f"{pkg} -run {run}", GO_TEST.format(pkg=pkg, run=run))


MUTATIONS = [
    # ---------- 清单的组装（owner：buildChanges） ----------
    {
        # 核对只看成功的步骤，而 file.write 是先截断再写：一步失败的写入
        # 完全可能已经留下真实改动。只按核对列清单，用户就以为盘上没动过。
        "name": "清单只按产物核对列（漏掉失败的写入）",
        "file": "internal/agent/changes.go",
        "edits": [(
            """	for _, r := range recs {
		key := pathKey(r.Path)
		if _, has := display[key]; !has {
			order = append(order, key)
			display[key] = r.Path
		}
	}
""", "")],
        "targets": [t(AGENT, "TestBuildChanges")],
    },
    {
        # 同一个路径被这个任务写过再改，还原的口径始终是"退到任务开始之前"。
        # 取最后一条等于只退一轮，而界面上一句"已退回到任务开始前"——那是撒谎。
        "name": "写前快照取最后一条（还原只退到中间状态）",
        "file": "internal/agent/preimage.go",
        "edits": [(
            """	want := pathKey(path)
	for _, r := range recs {
		if r.Path != "" && pathKey(r.Path) == want {
			return r, true
		}
	}""",
            """	want := pathKey(path)
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.Path != "" && pathKey(r.Path) == want {
			return r, true
		}
	}""")],
        "targets": [t(AGENT, "TestFirstPreImageOf"), t(AGENT, "TestBuildChanges")],
    },
    {
        "name": "一次移动不成对退（结果是把文件复制成两份）",
        "file": "internal/agent/preimage.go",
        "edits": [('	if first.Tool != "file.move" {', '	if first.Tool != "file.move.paired" {')],
        "targets": [t(AGENT, "TestPreImageGroup"), t(WEBUI, "TestChanges_MoveRevertsInPairs")],
    },
    {
        "name": "清单无改动时返回空切片而非 nil（两句话被压成一句）",
        "file": "internal/agent/changes.go",
        "edits": [("	if len(order) == 0 {\n		return nil\n	}",
                   "	if len(order) == 0 {\n		return []types.FileChange{}\n	}")],
        "targets": [t(AGENT, "TestBuildChanges_ReadOnlyTaskHasNilList")],
    },
    {
        # 源端（写前存在、现在不在）判不出 deleted，就只能落 touched。
        # 少说一层还可以救，说错一层不行——但 deleted 是界面上"删除"这个徽标的唯一来源。
        "name": "deleted 形态判不出来（一律落 touched）",
        "file": "internal/agent/changes.go",
        "edits": [(
            """	case want == wantAbsent:
		if prevExists {
			return kindDeleted
		}
		return kindTouched""",
            "	case want == wantAbsent:\n		return kindTouched")],
        "targets": [t(AGENT, "TestBuildChanges_Kinds")],
    },
    # ---------- 能不能还原（owner：preImage.reversible） ----------
    {
        "name": "reversible 恒真（内容没留住也报可还原）",
        "file": "internal/agent/preimage.go",
        "edits": [("	return !p.Exists || p.File != \"\"", "	return true")],
        "targets": [t(AGENT, "TestBuildChanges_BlockedReasonIsWhatUserSees")],
    },
    {
        # 非空目录里一定有不属于这次任务的东西。RemoveAll 下去，还原就成了删库。
        "name": "还原目录时递归删除",
        "file": "internal/agent/preimage.go",
        "edits": [("		if err := os.Remove(rec.Path); err != nil {",
                   "		if err := os.RemoveAll(rec.Path); err != nil {")],
        "targets": [t(AGENT, "TestRestorePreImage")],
    },
    # ---------- 两道闸与两处回填 ----------
    {
        # 去掉之后还原侧仍被 preImageGroup 兜住（纵深），但 diff 侧会放开：
        # 清单外的路径变成"可探测存在性、可读体量"的接口。所以判据落在 diff 那一条。
        "name": "去掉「路径必须在本次清单里」这道闸",
        "file": "internal/agent/webfacade.go",
        "edits": [(
            """	head, ok := firstPreImageOf(recs, target)
	if !ok {
		return changeRef{}, fmt.Errorf("这个路径不在本次任务的改动清单里，不能动")
	}""",
            "	head, _ := firstPreImageOf(recs, target)")],
        "targets": [t(WEBUI, "TestChanges_RejectsPathsOutsideTheList")],
    },
    {
        "name": "还原后不更新归档（重启后看不到已还原）",
        "file": "internal/agent/webfacade.go",
        "edits": [(
            """	if err := SaveTaskResult(a.Cfg.DataDir, g); err != nil {
		fmt.Fprintf(os.Stderr, "[gleam] 改动清单已还原但归档没更新（下次读档案会看不到这一位）：%v\\n", err)
	}
""", "")],
        "targets": [t(WEBUI, "TestChanges_FullLoop")],
    },
    {
        # 内存表优先于归档。不回填的话：点完还原、界面刷新一次，清单又变回"未还原"，
        # 而同一个任务在重启后反而显示已还原——两个方向相反的错答案。
        "name": "还原后不把归档回填内存",
        "file": "internal/webui/handlers.go",
        "edits": [(
            """	if archived, has := s.archivedTask(id); has {
		s.mu.Lock()
		if t, ok := s.tasks[id]; ok {
			t.Result = archived.Result
		}
		s.mu.Unlock()
		res["refreshed"] = true
	}
""", "")],
        "targets": [t(WEBUI, "TestChanges_FullLoop")],
    },
    # ---------- 接线与对比算法 ----------
    {
        # 快照层的 DataDir 来自配置。这一处漏掉，清单会照常列出每一行、
        # 照常报"没有写前快照"，界面上看不出任何异常——只有还原按不下去。
        "name": "DataDir 没传进执行器（快照整层不生效）",
        "file": "internal/agent/agent.go",
        "edits": [(
            """			DataDir:        a.Cfg.DataDir,
			OnUsage:        a.BumpUsage,
			// 运行中落盘""",
            """			OnUsage:        a.BumpUsage,
			// 运行中落盘""")],
        "targets": [t(AGENT, "TestRunGoal_ChangesReachResult")],
    },
    {
        # Windows 上文件常是 CRLF。不去 \\r，同一份内容换过一次行尾就会让**每一行**
        # 变成"删了又加"，那种 diff 比不显示更坏。
        "name": "切行不去 \\\\r（CRLF 文件每次都是全删全加）",
        "file": "internal/agent/difftext.go",
        "edits": [('	parts := strings.Split(strings.ReplaceAll(s, "\\r\\n", "\\n"), "\\n")',
                   '	parts := strings.Split(s, "\\n")')],
        "targets": [t(AGENT, "TestDiffText_CRLFSameContentIsNoChange"), t(AGENT, "TestSplitLines")],
    },
    {
        "name": "对比忘了加回公共前缀的行号偏移",
        "file": "internal/agent/difftext.go",
        "edits": [("	res.Lines = append(res.Lines, lcsLines(midA, midB, head)...)",
                   "	res.Lines = append(res.Lines, lcsLines(midA, midB, 0)...)")],
        "targets": [t(AGENT, "TestDiffText_AddDelAndLineNumbers")],
    },
    {
        # 快照记录器归**任务**，一次目标可以跑好几轮 Execute（重规划、回落直聊）。
        # 不接回上一轮的清单，第二轮就只看得到自己：真实写出去的文件从清单里消失，
        # 界面对着一份被写过的盘说"没动过"——这一层唯一不许出现的方向。
        # 另一半：firstPreImageOf 取最早那份，接不上上一轮，"写前"就成了上一轮写出的内容。
        # 这一条是真机走查抓出来的（临时实例里第一轮写 note.md、第二轮只剩回复）。
        "name": "新记录器不回读上一轮的快照（重规划后漏报）",
        "file": "internal/agent/preimage.go",
        "edits": [("	s.load()\n", "")],
        "targets": [t(AGENT, "TestSnapshot_SurvivesReplan")],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 G：改动清单与写前还原的负例控制", MUTATIONS,
                       timeout=240, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
