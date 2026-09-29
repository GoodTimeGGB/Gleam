package agent

import (
	"os"

	"gleam/pkg/types"
)

// 改动清单：把"这次任务动过哪些路径、能不能退回去"说给用户听。
//
// **两份来源、各管一半**：
//   - 产物核对（`verifyArtifacts`）按路径收敛，知道每个路径**最终**应该是什么样、实测是什么样；
//   - 写前快照（`preImage`）知道每个路径**动手之前**是什么样，是能还原的依据。
//
// 清单以**快照为主干**、核对为补充，而不是反过来：核对只看成功的步骤
// （`verifyArtifacts` 开头那句 `Status != StepSucceeded` 就 continue 了），
// 而 `file.write` 是先 `O_TRUNC` 再写——一步失败的写入完全可能已经把原文件截断。
// 那时"没做成"的步骤恰恰留下一处真实的改动，只按核对列清单就会漏报，
// 而漏报的方向是"用户以为盘上没动过"，比误报更坏。
//
// 没配数据目录时快照是空的，退回以核对为主干：清单仍然有，只是每行都写"不可还原"。

// buildChanges 组装一次任务的净改动清单。无改动时返回 nil（不是空切片）：
// "这个任务没动文件"与"动了但列不出来"在界面上是两句话。
func buildChanges(steps []types.StepResult, recs []preImage) []types.FileChange {
	facts := verifyArtifacts(steps)
	// 按 pathKey 归并而不是按原始字符串：同一个路径在 Windows 上可能有大小写或
	// 分隔符不同的两种写法，分成两行就等于把一次改动数成了两次。
	byPath := make(map[string]artifactFact, len(facts))
	display := make(map[string]string, len(facts)+len(recs))
	order := make([]string, 0, len(facts)+len(recs))
	for _, f := range facts {
		key := pathKey(f.path)
		if _, seen := display[key]; !seen {
			order = append(order, key)
			display[key] = f.path
		}
		byPath[key] = f
	}
	for _, r := range recs {
		key := pathKey(r.Path)
		if _, has := display[key]; !has {
			order = append(order, key)
			display[key] = r.Path
		}
	}
	if len(order) == 0 {
		return nil
	}

	out := make([]types.FileChange, 0, len(order))
	for _, key := range order {
		p := display[key]
		f, hasFact := byPath[key]
		rec, hasRec := firstPreImageOf(recs, p)
		ch := types.FileChange{Path: p}
		switch {
		case hasFact:
			ch.Tool, ch.StepID = f.tool, f.stepID
			ch.OK, ch.Note, ch.Bytes = f.ok, f.detail, f.size
			ch.Kind = changeKind(f.tool, f.want, hasRec && rec.Exists)
		case hasRec:
			// 留了写前快照、却没有核对结论 = 那一步没成功，但盘可能已经被动过。
			ch.Tool, ch.StepID = rec.Tool, rec.StepID
			ch.Kind = kindTouched
			ch.Note = "该步骤未成功，产物核对没有结论；写前快照留着，可还原"
		}
		if hasRec {
			ch.PrevBytes = rec.Bytes
			ch.Reversible = rec.reversible()
			if !ch.Reversible {
				ch.Blocked = rec.Reason
				if ch.Blocked == "" {
					ch.Blocked = "写前内容未留存，无法还原"
				}
			}
		} else {
			ch.Blocked = "没有写前快照（未配置数据目录，或这一步在快照之前就被跳过）"
		}
		// 只有快照、没有核对结论时，现字节数没人实测过——现取一次。
		// 报 0 会被读成"这个文件空了"，那是凭空造出来的错误结论。
		if !hasFact {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				ch.Bytes = info.Size()
			}
		}
		out = append(out, ch)
	}
	return out
}

// 改动类型的取值（界面负责把它们译成人话，见 webui 前端的 CH_KIND_LABELS）。
const (
	kindAdded    = "added"    // 本任务之前这里没有东西
	kindModified = "modified" // 本任务之前这里就有内容
	kindDeleted  = "deleted"  // 现在不在了
	kindDir      = "dir"      // 目录
	kindMoved    = "moved"    // 移动的目标端（源端单列一条 deleted）
	kindTouched  = "touched"  // 动过，但说不出是哪一类（步骤失败、或形态判不准）
)

// changeKind 由「工具 + 期望形态 + 写前在不在」定出这一行的类型。
//
// 三个都要问：光看期望形态分不出"新建"还是"覆盖"（那要问写前），
// 也分不出"删除"还是"移动走了"（那要问是哪个工具）。
// 拿不准的一律落 `touched`——清单宁可少说一层，也不许说错一层。
func changeKind(tool, want string, prevExists bool) string {
	switch {
	case tool == "file.move" && want == wantExists:
		return kindMoved
	case want == wantAbsent:
		if prevExists {
			return kindDeleted
		}
		return kindTouched
	case want == wantDir:
		return kindDir
	case want == wantFile:
		if prevExists {
			return kindModified
		}
		return kindAdded
	default:
		return kindTouched
	}
}
