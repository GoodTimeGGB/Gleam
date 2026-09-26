// pending.go 等待审批的任务状态落盘。
//
// 为什么只做一半（按清单判定）：完整断点续跑成本最高、收益未验证——Gleam 是桌面单机，
// 进程活着才有人点审批。但"进程死在等待审批期间"是真实存在的：用户关掉窗口、
// 升级重启，那条任务就永远没人知道它卡在哪一步、要批什么。所以只做一件事：
// **把等待审批中的步骤落盘，重启后能列出来，人工重新提交**。不做状态机恢复。
package safety

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gleam/internal/atomicfile"
)

// PendingApproval 一条"等待用户批准"的记录。
type PendingApproval struct {
	TaskID string    `json:"task_id"`
	StepID string    `json:"step_id"`
	Tool   string    `json:"tool"`
	Risk   string    `json:"risk"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// SetPendingPath 指定等待审批记录的落盘文件；空路径 = 关闭落盘（默认）。
func (g *Gate) SetPendingPath(p string) {
	if g == nil {
		return
	}
	g.pendingMu.Lock()
	g.pendingPath = p
	g.pendingMu.Unlock()
}

// MarkPending 记录一条等待审批（进程若在此期间退出，重启后仍能看到它）。
func (g *Gate) MarkPending(e PendingApproval) {
	if g == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	// 同一 (任务, 步骤) 重复登记只保留最新一条：重规划会重新走同一步
	for i := range g.pending {
		if g.pending[i].TaskID == e.TaskID && g.pending[i].StepID == e.StepID {
			g.pending[i] = e
			g.savePendingLocked()
			return
		}
	}
	g.pending = append(g.pending, e)
	g.savePendingLocked()
}

// ClearPending 移除一条等待审批（审批已有结果：批准/拒绝/超时/取消）。
func (g *Gate) ClearPending(taskID, stepID string) {
	if g == nil {
		return
	}
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	out := g.pending[:0]
	for _, p := range g.pending {
		if p.TaskID == taskID && p.StepID == stepID {
			continue
		}
		out = append(out, p)
	}
	if len(out) == len(g.pending) {
		return // 没有变化，不落盘
	}
	g.pending = out
	g.savePendingLocked()
}

// PendingApprovals 返回当前等待审批的记录（按登记时间正序）。
func (g *Gate) PendingApprovals() []PendingApproval {
	if g == nil {
		return nil
	}
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	out := append([]PendingApproval(nil), g.pending...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// LoadPendingApprovals 直接读落盘文件（供重启后的 CLI / 界面展示，不依赖 Gate 实例）。
func LoadPendingApprovals(path string) ([]PendingApproval, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PendingApproval
	if err := json.Unmarshal(b, &out); err != nil {
		// 损坏文件不阻断：等待审批记录是辅助信息，读不出来就当没有
		return nil, nil
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// ClearPendingFile 清空落盘文件（人工重新提交后调用）。
func ClearPendingFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// savePendingLocked 原子落盘（调用方须持有 pendingMu）。
func (g *Gate) savePendingLocked() {
	if g.pendingPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.pendingPath), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(g.pending, "", " ")
	if err != nil {
		return
	}
	_ = atomicfile.Write(g.pendingPath, data, 0o644)
}
