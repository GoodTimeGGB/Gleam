package growth

import (
	"path/filepath"
	"testing"
)

// 重试率 / 中断率（P3-4）：分母 = 完成 + 中断，边界写清才可比。
func TestStats_RetryAbort(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Entry{Type: "task_completed", Goal: "任务甲", Score: 90})
	l.Record(Entry{Type: "task_completed", Goal: "任务乙（重试后才成）", Score: 85, Retried: true})
	l.Record(Entry{Type: "task_completed", Goal: "任务丙（又是一次重试）", Score: 80, Retried: true})
	l.Record(Entry{Type: "task_aborted", Goal: "任务丁，用户不等了"})
	l.Record(Entry{Type: "skill_used", SkillName: "x"}) // 不进分母

	st := l.Stats()
	if st.TotalTasks != 3 {
		t.Errorf("TotalTasks = %d，应为 3（task_aborted 不计入完成数）", st.TotalTasks)
	}
	if st.RetryCount != 2 {
		t.Errorf("RetryCount = %d，应为 2", st.RetryCount)
	}
	if st.AbortedTasks != 1 {
		t.Errorf("AbortedTasks = %d，应为 1", st.AbortedTasks)
	}
	// 分母 = 完成 3 + 中断 1 = 4
	if st.RetryRate != 0.5 {
		t.Errorf("RetryRate = %.2f，应为 0.50（2/4）", st.RetryRate)
	}
	if st.AbortRate != 0.25 {
		t.Errorf("AbortRate = %.2f，应为 0.25（1/4）", st.AbortRate)
	}
}

// 空日志不算除零。
func TestStats_RetryAbortEmpty(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "growth.json"))
	st := l.Stats()
	if st.RetryRate != 0 || st.AbortRate != 0 {
		t.Errorf("空日志比率应为 0: %+v", st)
	}
}
