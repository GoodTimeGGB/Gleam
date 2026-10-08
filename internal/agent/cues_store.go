package agent

// cues_store.go — 候补目标的**处置记录**：只有人的决定才落盘，卡片本身不落盘。
//
// 卡片是任务历史的一个函数（见 cues.go），历史没变重算一次还是那几张；要落盘的只有
// 「别再提」和「已采纳」。反过来说：卡片若也落盘，它就必须有人负责删除，而
// 「登记类结构的消失不跟着它所服务的事走」是这个仓库反复栽过的那一条。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gleam/internal/atomicfile"
)

// CueSuppression 一条处置记录。
type CueSuppression struct {
	Fingerprint string `json:"fingerprint"`
	Signal      string `json:"signal"`
	Title       string `json:"title"`
	Goal        string `json:"goal"`
	At          string `json:"at"`
	Reason      string `json:"reason"` // dismissed 别再提 · adopted 已采纳
}

const (
	cueDismissed = "dismissed"
	cueAdopted   = "adopted"
)

// cueStore 内存里的一份处置表，落盘在 <DataDir>/cues.json。
type cueStore struct {
	mu   sync.Mutex
	path string
	Sup  []CueSuppression

	// lastErr 上一次写盘失败的原因。读侧要能回答「为什么这条别再提没生效」——
	// 静默吞掉写失败等于替用户决定「它其实还会再来」。
	lastErr error
}

func cueStorePath(dataDir string) string {
	return filepath.Join(dataDir, "cues.json")
}

// loadCueStore 读一次处置表。
//
// 文件不存在 = 还没人做过任何处置，这是常态不是错误；读不动 = 记下来并照空表走，
// 让这一页照常算得出卡：坏一条记录不该把整页变成「没有线索」。
func loadCueStore(dataDir string) *cueStore {
	st := &cueStore{path: cueStorePath(dataDir)}
	b, err := os.ReadFile(st.path)
	if err != nil {
		if !os.IsNotExist(err) {
			st.lastErr = err
		}
		return st
	}
	var rows []CueSuppression
	if err := json.Unmarshal(b, &rows); err != nil {
		st.lastErr = err
		return st
	}
	st.Sup = rows
	return st
}

func (s *cueStore) muted(fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.Sup {
		if x.Fingerprint == fp {
			return true
		}
	}
	return false
}

// list 按时间倒序给出处置记录：撤销入口在界面末尾，最新的那条才够得着。
//
// 返回的是**给视图的那一份**：多补两句人话（信号与原因）。落盘那份不带它们——
// 盘上只存取值，显示口径每次现取，这样加一种取值不会留下"旧记录在新界面裸奔"。
func (s *cueStore) list() []CueSuppressedRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CueSuppressedRow, 0, len(s.Sup))
	for _, x := range s.Sup {
		out = append(out, CueSuppressedRow{
			CueSuppression: x,
			SignalText:     cueSignalText(x.Signal),
			ReasonText:     cueReasonText(x.Reason),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

// cueReasonText 处置原因的人话。取值只有两个，但这里仍然是一处一份：
// 界面按这个字段印，别处再译一版就会和记录它对不上。
func cueReasonText(reason string) string {
	switch reason {
	case cueDismissed:
		return "别再提"
	case cueAdopted:
		return "已采纳过"
	}
	return reason
}

// note 把写盘失败如实说给界面。
func (s *cueStore) note() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastErr == nil {
		return ""
	}
	return fmt.Sprintf("处置记录上一次没写下去（眼下的判断仍然生效，重启后可能重来）：%v", s.lastErr)
}

// record 记一条处置并落盘；同一指纹重复记只留最新的一条。
func (s *cueStore) record(row CueRow, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]CueSuppression, 0, len(s.Sup)+1)
	for _, x := range s.Sup {
		if x.Fingerprint != row.ID {
			kept = append(kept, x)
		}
	}
	s.Sup = append(kept, CueSuppression{
		Fingerprint: row.ID,
		Signal:      row.Signal,
		Title:       row.Title,
		Goal:        row.Goal,
		At:          time.Now().Format(time.RFC3339),
		Reason:      reason,
	})
	return s.flushLocked()
}

// clear 撤掉一条处置：点错「别再提」要能反悔，否则抑制会变成永久静默。
func (s *cueStore) clear(fp string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]CueSuppression, 0, len(s.Sup))
	found := false
	for _, x := range s.Sup {
		if x.Fingerprint == fp {
			found = true
			continue
		}
		kept = append(kept, x)
	}
	s.Sup = kept
	if !found {
		return false, nil
	}
	return true, s.flushLocked()
}

func (s *cueStore) flushLocked() error {
	b, err := json.MarshalIndent(s.Sup, "", "  ")
	if err != nil {
		s.lastErr = err
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		s.lastErr = err
		return err
	}
	// 0600：这份表存的是目标原文，属于「用户说过什么」那一类，与写前快照同权限。
	if err := atomicfile.Write(s.path, b, 0o600); err != nil {
		s.lastErr = err
		return err
	}
	s.lastErr = nil
	return nil
}

// ---------- 门面（供接入层调用） ----------

// CueDismiss 「别再提这一条」：落盘，之后每次重算都按指纹过滤掉。
func (a *Agent) CueDismiss(id string) error { return a.cueRecord(id, cueDismissed) }

// CueAdopt 记一条「这条已经交给用户了」——它**不提交任务**。
//
// 采纳的实际动作发生在输入区：界面把 goal 填进去，按回车的那一下才是执行，
// 那一次会留下正常的任务归档。这里只负责让这张卡不再重复浮出来。
func (a *Agent) CueAdopt(id string) error { return a.cueRecord(id, cueAdopted) }

func (a *Agent) cueRecord(id, reason string) error {
	if id == "" {
		return fmt.Errorf("缺少候补目标编号")
	}
	for _, row := range a.cueRowsAll() {
		if row.ID == id {
			return loadCueStore(a.Cfg.DataDir).record(row, reason)
		}
	}
	return fmt.Errorf("这条候补目标已经不成立了（历史变了，或它已被裁剪）")
}

// CueUnsuppress 撤销一条处置：对应的卡会重新浮出来（前提是那段历史还在）。
func (a *Agent) CueUnsuppress(fp string) error {
	if fp == "" {
		return fmt.Errorf("缺少处置记录编号")
	}
	st := loadCueStore(a.Cfg.DataDir)
	ok, err := st.clear(fp)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("没有这条处置记录")
	}
	return nil
}

// cueRowsAll 忽略抑制算出全部卡片：处置入口要能找到**刚被记进去**的那张卡，
// 而 CueView 是给人看的那一份（已过滤、已截配额）。
func (a *Agent) cueRowsAll() []CueRow {
	results, _, err := ListTaskResults(a.Cfg.DataDir, cueTaskScan)
	if err != nil {
		return nil
	}
	rows := deriveCues(results, &cueStore{})
	return rows
}
