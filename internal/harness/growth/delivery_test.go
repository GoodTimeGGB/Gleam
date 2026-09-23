package growth

import (
	"math"
	"path/filepath"
	"testing"
)

// 首次验收通过率 / 累计返工轮数（P5）：产物是不是一次就合格。
//
// 与重试率、中断率互补：那两个是"用户替你打分"，回答满不满意；
// 这一组回答"系统一次做对的比例有多高"。
func TestStats_FirstPassAndReworks(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 工作模式完成：两条一次就合格、一条返工两轮才成
	l.Record(Entry{Type: "task_completed", Goal: "甲", Score: 90, Delivery: true, FirstPass: true})
	l.Record(Entry{Type: "task_completed", Goal: "乙", Score: 88, Delivery: true, FirstPass: true})
	l.Record(Entry{Type: "task_completed", Goal: "丙（返工两轮才成）", Score: 80, Delivery: true, Reworks: 2})
	// 对话模式完成：没有产物可核，**不进**交付口径的分母
	l.Record(Entry{Type: "task_completed", Goal: "闲聊两句", Score: 100})
	// 中断的任务：中断前已经返工过一轮，那笔成本是真花掉的
	l.Record(Entry{Type: "task_aborted", Goal: "丁，用户不等了", Reworks: 1})

	st := l.Stats()
	if st.TotalTasks != 4 {
		t.Errorf("TotalTasks = %d，应为 4", st.TotalTasks)
	}
	if st.FirstPassBase != 3 {
		t.Errorf("FirstPassBase = %d，应为 3（只算记过口径的完成任务，对话任务不进分母）", st.FirstPassBase)
	}
	if st.FirstPassCount != 2 {
		t.Errorf("FirstPassCount = %d，应为 2", st.FirstPassCount)
	}
	if want := 2.0 / 3.0; math.Abs(st.FirstPassRate-want) > 1e-9 {
		t.Errorf("FirstPassRate = %.4f，应为 %.4f（2/3）", st.FirstPassRate, want)
	}
	// 2（返工过的完成任务）+ 1（中断前返工）= 3：只统计"最后成功了"的那些会把浪费藏起来
	if st.ReworksTotal != 3 {
		t.Errorf("ReworksTotal = %d，应为 3（含中断任务中断前已发生的返工）", st.ReworksTotal)
	}
}

// 没有记过交付口径的完成任务时，分母为零 → 比率保持 0，不是 NaN。
//
// 老条目（本口径落地前记的）与对话任务都落在这里：把它们算成"没通过首次验收"，
// 比率就会随历史长度和使用习惯漂移，而不是随交付质量漂移。
func TestStats_FirstPassRateNoBase(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "growth.json"))
	l.Record(Entry{Type: "task_completed", Goal: "闲聊", Score: 100})       // 对话：不进分母
	l.Record(Entry{Type: "task_completed", Goal: "老条目，没记过口径", Score: 70}) // 历史：不进分母

	st := l.Stats()
	if st.FirstPassBase != 0 {
		t.Errorf("FirstPassBase = %d，应为 0", st.FirstPassBase)
	}
	if st.FirstPassRate != 0 {
		t.Errorf("FirstPassRate = %.4f，无分母时应为 0（不是 NaN）", st.FirstPassRate)
	}
	if st.ReworksTotal != 0 {
		t.Errorf("ReworksTotal = %d，应为 0", st.ReworksTotal)
	}
}

// 空日志不算除零。
func TestStats_FirstPassEmpty(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "growth.json"))
	st := l.Stats()
	if st.FirstPassRate != 0 || st.FirstPassBase != 0 || st.ReworksTotal != 0 {
		t.Errorf("空日志的交付口径应为零值: %+v", st)
	}
}
