package scheduler

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScheduler_AddJobValidation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "schedules.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, cron string
		interval   int
		goal       string
		wantErr    bool
	}{
		{"ok-cron", "0 9 * * *", 0, "目标", false},
		{"ok-interval", "", 30, "目标", false},
		{"no-trigger", "", 0, "目标", true},
		{"no-goal", "0 9 * * *", 0, "", true},
		{"bad-cron", "99 * * * *", 0, "目标", true},
		{"tiny-interval", "", 1, "目标", true},
		{"bad-mode", "0 9 * * *", 0, "目标", true},
	}
	_ = cases[6]
	if _, err := s.AddJob("bad-mode", "0 9 * * *", 0, "目标", "weird"); err == nil {
		t.Error("非法模式应报错")
	}
	for i, c := range cases[:6] {
		_, err := s.AddJob(c.name, c.cron, c.interval, c.goal, "auto")
		if (err != nil) != c.wantErr {
			t.Errorf("case %d %s: err=%v wantErr=%v", i, c.name, err, c.wantErr)
		}
	}
	if s.Count() != 2 {
		t.Errorf("Count = %d", s.Count())
	}
}

// TestScheduler_DuplicateNameRejected 任务名是主键：同名再次创建必须报错，
// 而不是静默覆盖已有任务（2026-09-24 接口走查 L1）。
func TestScheduler_DuplicateNameRejected(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "schedules.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddJob("daily", "0 9 * * *", 0, "整理桌面", "auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddJob("daily", "*/5 * * * *", 0, "覆盖用目标", "auto"); err == nil {
		t.Fatal("同名任务应被拒绝，而不是静默替换")
	}
	jobs := s.ListJobs()
	if len(jobs) != 1 || jobs[0].Goal != "整理桌面" || jobs[0].Cron != "0 9 * * *" {
		t.Fatalf("拒绝后原任务不应被改动: %+v", jobs)
	}
}

// TestScheduler_JobNameMustStayAddressable 任务名是 REST 路径参数，写入口就得把它钉住。
//
// 反例是真实存在的：名字里带 `/` 的任务能按时跑，但 `/api/schedules/{name}/enabled`
// 在**解码后**的路径上匹配，`a%2Fb` 也会还原成两个 segment，路由不命中返回纯文本 404
// ——于是它既停不掉也删不掉，只能手动改 jobs 文件。
func TestScheduler_JobNameMustStayAddressable(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "schedules.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"每日巡检 9 点", false}, // 中文、数字、空格都是正常说法
		{"check.disk", false},
		{"backup_2026-09", false},
		{"每日巡检 09:00", true}, // 冒号不在允许集合里：宁可写清楚，也不要两套口径
		{"巡检：每日", true},      // 全角冒号同理，它也不算 \p{Han}
		{"巡检/每日", true},
		{"a/b", true},
		{"a\\b", true},
		{"../../etc/passwd", true},
		{"带?查询", true},
		{"带#锚点", true},
		{"带\x00控制符", true},
		{strings.Repeat("巡检", 33), true}, // 66 字，超长
	}
	for _, c := range cases {
		_, err := s.AddJob(c.name, "0 9 * * *", 0, "目标", "auto")
		if (err != nil) != c.wantErr {
			t.Errorf("名字 %q: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
	// 只统计合法的那几条：被拒的不该在列表里留下半个影子
	if got := s.Count(); got != 3 {
		t.Errorf("被拒名字不该入库，Count = %d，期望 3", got)
	}
}

func TestScheduler_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedules.json")
	s, _ := Open(path, nil)
	if _, err := s.AddJob("daily", "0 9 * * *", 0, "整理桌面", "auto"); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs := s2.ListJobs()
	if len(jobs) != 1 || jobs[0].Name != "daily" || jobs[0].Goal != "整理桌面" {
		t.Fatalf("重载任务 = %v", jobs)
	}
	if jobs[0].NextRun == nil {
		t.Error("重载后应补算 NextRun")
	}
}

func TestScheduler_AddJobDetailedPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedules.json")
	s, _ := Open(path, nil)
	if _, err := s.AddJobDetailed("nl", "30 18 * * 1-5", 0, "整理下载目录", "auto", "工作日18:30", "工作日 18:30（周一至周五）"); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs := s2.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("任务数 = %d", len(jobs))
	}
	if jobs[0].WhenText != "工作日18:30" || jobs[0].ScheduleText == "" {
		t.Errorf("自然语言时间说明未持久化: when=%q text=%q", jobs[0].WhenText, jobs[0].ScheduleText)
	}
}

func TestScheduler_FireOnTick(t *testing.T) {
	var mu sync.Mutex
	fired := []Job{}
	s, _ := Open(filepath.Join(t.TempDir(), "schedules.json"), nil)
	s.SetFire(func(j Job) { mu.Lock(); fired = append(fired, j); mu.Unlock() })

	if _, err := s.AddJob("soon", "", 5, "每5秒目标", "auto"); err != nil {
		t.Fatal(err)
	}
	job := s.ListJobs()[0]
	// 直接推进时钟调用 tick（NextRun = now+5s）
	s.tick(time.Now().Add(6 * time.Second))
	time.Sleep(100 * time.Millisecond) // fire 在独立 goroutine 中执行
	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 {
		t.Fatalf("触发次数 = %d", len(fired))
	}
	if fired[0].Goal != "每5秒目标" {
		t.Errorf("触发目标 = %q", fired[0].Goal)
	}
	// OneShot 触发后自动停用
	if _, err := s.AddJob("once", "0 9 * * *", 0, "一次性", "auto"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.jobs["once"].OneShot = true
	s.jobs["once"].NextRun = ptrTime(time.Now().Add(-time.Minute))
	s.mu.Unlock()
	s.tick(time.Now())
	jobs := s.ListJobs()
	for _, j := range jobs {
		if j.Name == "once" && j.Enabled {
			t.Error("OneShot 触发后应停用")
		}
	}
	_ = job
}

func TestScheduler_WatchTrigger(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "watched.txt")
	os.WriteFile(file, []byte("v1"), 0o644)

	var mu sync.Mutex
	fired := 0
	s, _ := Open(filepath.Join(t.TempDir(), "s.json"), nil)
	s.SetFire(func(j Job) { mu.Lock(); fired++; mu.Unlock() })

	if _, err := s.AddJob("watcher", "", 0, "文件变了", "auto"); err == nil {
		t.Error("缺少触发源应报错")
	}
	s.mu.Lock()
	s.jobs["watcher"] = &Job{Name: "watcher", WatchPath: file, Goal: "文件变了", Enabled: true}
	s.mu.Unlock()

	s.tick(time.Now()) // 建立快照
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(file, []byte("v2 longer"), 0o644)
	s.tick(time.Now().Add(1 * time.Second)) // 检测变化 → 触发
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	n := fired
	mu.Unlock()
	if n != 1 {
		t.Errorf("监听触发次数 = %d", n)
	}
	// 10 秒去抖内不重复触发
	s.tick(time.Now().Add(2 * time.Second))
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if fired != 1 {
		t.Errorf("去抖期内不应再次触发: %d", fired)
	}
}

func TestScheduler_EnableDisableDelete(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "s.json"), nil)
	s.AddJob("j1", "0 9 * * *", 0, "g", "auto")
	if err := s.SetEnabled("j1", false); err != nil {
		t.Fatal(err)
	}
	jobs := s.ListJobs()
	if jobs[0].Enabled {
		t.Error("应已停用")
	}
	if err := s.SetEnabled("nope", true); err == nil {
		t.Error("不存在的任务应报错")
	}
	ok, err := s.DeleteJob("j1")
	if err != nil || !ok {
		t.Fatalf("DeleteJob = %v %v", ok, err)
	}
	if s.Count() != 0 {
		t.Error("删除后应为空")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
