package scheduler

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 设计文档 §4.2.5：事件触发之 HTTP 回调——TriggerNow 立即执行指定任务。

func TestScheduler_TriggerNow(t *testing.T) {
	var mu sync.Mutex
	fired := []string{}
	s, err := Open(filepath.Join(t.TempDir(), "s.json"), func(j Job) {
		mu.Lock()
		fired = append(fired, j.Name)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddJob("hook-job", "", 3600, "回调目标", "auto"); err != nil {
		t.Fatal(err)
	}

	ok, err := s.TriggerNow("hook-job")
	if err != nil || !ok {
		t.Fatalf("TriggerNow = %v, %v", ok, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(fired)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 || fired[0] != "hook-job" {
		t.Errorf("触发记录 = %v", fired)
	}
	jobs := s.ListJobs()
	if jobs[0].LastRun == nil {
		t.Error("LastRun 应被更新")
	}
}

func TestScheduler_TriggerNowErrors(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "s.json"), nil)
	s.AddJob("j1", "0 9 * * *", 0, "g", "auto")

	if _, err := s.TriggerNow("nope"); err == nil {
		t.Error("不存在的任务应报错")
	}
	_ = s.SetEnabled("j1", false)
	if _, err := s.TriggerNow("j1"); err == nil {
		t.Error("已停用任务应报错")
	}
}
