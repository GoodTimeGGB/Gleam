// Package scheduler 实现轻量级任务调度器：
// 基于 time.Ticker 的一分钟心跳，支持 Cron 表达式、固定间隔与文件变化（轮询）触发。
// 任务持久化为 JSON，重启后自动恢复并补算下次触发时间。
package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"gleam/internal/atomicfile"
)

// JobNamePattern 任务名的合法形状。
//
// **为什么任务名不能有斜杠**：它是 REST 路径参数（/api/schedules/{name}/enabled）。
// Go 的 mux 在**解码后**的路径上匹配，`a/b` 编码成 `a%2Fb` 也会先还原成两个 segment
// ——路由直接不命中，返回纯文本 404。结果是这个任务能按时跑，却永远删不掉、停不了，
// 只能去改 jobs 文件。名字是主键，就得在写入口把它钉成可寻址的形状。
var JobNamePattern = regexp.MustCompile(`^[\w\p{Han}\-. ]{1,64}$`)

// Job 定时任务。
type Job struct {
	Name         string     `json:"name"`
	Cron         string     `json:"cron,omitempty"`
	IntervalSec  int        `json:"interval_sec,omitempty"`
	WatchPath    string     `json:"watch_path,omitempty"` // 文件/目录变化触发
	WatchPattern string     `json:"watch_pattern,omitempty"`
	Goal         string     `json:"goal"`
	Mode         string     `json:"mode,omitempty"`
	Notify       string     `json:"notify,omitempty"` // 跑完要不要通知（空 = 默认 on_failure）
	Enabled      bool       `json:"enabled"`
	OneShot      bool       `json:"one_shot,omitempty"`      // 触发一次后自动禁用
	WhenText     string     `json:"when_text,omitempty"`     // 用户原始时间说法（自然语言）
	ScheduleText string     `json:"schedule_text,omitempty"` // 解析后的人读调度说明
	LastRun      *time.Time `json:"last_run,omitempty"`
	NextRun      *time.Time `json:"next_run,omitempty"`
}

// 通知策略：任务跑完之后要不要打扰用户。
//
// 为什么默认是「成功静默、只报异常」：定时任务的整个价值就是"不用盯着"。
// 一个每小时跑一次的巡检每次都弹通知，等于让用户继续盯着——那还不如不做定时；
// 反过来，失败不响就等于没有巡检：用户会以为一切正常，而它已经坏了很久。
// 想每次都知道的用户显式设 always。
const (
	NotifyOnFailure = "on_failure" // 默认：成功静默，失败才通知
	NotifyAlways    = "always"     // 每次都通知
	NotifyNever     = "never"      // 从不通知（结果仍可在任务记录里查）
)

// NormalizeNotify 归一化通知策略：空值取默认，非法值报错。
func NormalizeNotify(v string) (string, error) {
	switch trim(v) {
	case "":
		return NotifyOnFailure, nil
	case NotifyOnFailure, NotifyAlways, NotifyNever:
		return trim(v), nil
	}
	return "", fmt.Errorf("非法通知策略 %q（可用：%s / %s / %s）", v, NotifyAlways, NotifyOnFailure, NotifyNever)
}

// ShouldNotify 这次跑完要不要通知。
//
// 为什么是 Job 上的一个方法，而不是散在调用点：策略只有一份实现。
// 否则"什么时候该打扰用户"会在多处各判一次，然后漂移——而漂移的方向通常是
// 「某个调用点忘了判」，于是失败被静默掉，正好是最不能静默的那一类。
//
// 存量任务里若存了非法值，按默认走：**别让一个脏字段把失败通知静默掉**。
func (j Job) ShouldNotify(succeeded bool) bool {
	policy, err := NormalizeNotify(j.Notify)
	if err != nil {
		policy = NotifyOnFailure
	}
	switch policy {
	case NotifyNever:
		return false
	case NotifyAlways:
		return true
	default:
		return !succeeded
	}
}

// FireFunc 到点回调（由运行时注入，通常为提交一个目标）。
type FireFunc func(j Job)

// Scheduler 调度器。
type Scheduler struct {
	mu      sync.Mutex
	path    string
	jobs    map[string]*Job
	fire    FireFunc
	stop    chan struct{}
	running bool

	watch    map[string]watchSnap // 文件监听快照
	lastFire map[string]time.Time // 变化触发去抖
}

type watchSnap struct {
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
	Count   int       `json:"count"`
}

// Open 加载调度任务（文件不存在则空库）。
func Open(path string, fire FireFunc) (*Scheduler, error) {
	s := &Scheduler{
		path:     path,
		jobs:     map[string]*Job{},
		fire:     fire,
		stop:     make(chan struct{}),
		watch:    map[string]watchSnap{},
		lastFire: map[string]time.Time{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		_ = os.Rename(path, path+".corrupt")
		return s, nil
	}
	now := time.Now()
	for _, j := range jobs {
		if j == nil || j.Name == "" {
			continue
		}
		s.recomputeNext(j, now)
		s.jobs[j.Name] = j
	}
	return s, nil
}

// SetFire 设置/替换触发回调（装配期延迟绑定）。
func (s *Scheduler) SetFire(f FireFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fire = f
}

// Start 启动心跳循环。
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	go s.loop()
}

// Stop 停止调度。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stop)
}

func (s *Scheduler) loop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

// tick 每秒检查到期任务（精确到分钟级 Cron、秒级 interval/监听）。
func (s *Scheduler) tick(now time.Time) {
	var due []*Job
	s.mu.Lock()
	fire := s.fire
	for _, j := range s.jobs {
		if !j.Enabled {
			continue
		}
		if j.NextRun != nil && !now.Before(*j.NextRun) {
			due = append(due, j)
			continue
		}
		if j.WatchPath != "" {
			s.checkWatch(j, now)
		}
	}
	for _, j := range due {
		nowT := now
		j.LastRun = &nowT
		s.recomputeNext(j, now)
		if j.OneShot {
			j.Enabled = false
			j.NextRun = nil
		}
	}
	if len(due) > 0 {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	for _, j := range due {
		if fire != nil {
			j := *j
			go fire(j)
		}
	}
}

// checkWatch 轮询监听路径，变化时触发（带 10 秒去抖）。
func (s *Scheduler) checkWatch(j *Job, now time.Time) {
	snap, _ := s.snapshot(j.WatchPath, j.WatchPattern)
	if prev, ok := s.watch[j.Name]; ok && snap != prev {
		if last, ok2 := s.lastFire[j.Name]; !ok2 || now.Sub(last) > 10*time.Second {
			nowT := now
			j.LastRun = &nowT
			s.lastFire[j.Name] = now
			if j.NextRun == nil {
				s.recomputeNext(j, now)
			}
			if s.fire != nil {
				job := *j
				go s.fire(job)
			}
		}
	}
	s.watch[j.Name] = snap
}

// snapshot 计算路径快照：单文件取 mtime/size；目录取 mtime/size/条目数。
func (s *Scheduler) snapshot(path, pattern string) (watchSnap, watchSnap) {
	info, err := os.Stat(path)
	if err != nil {
		return watchSnap{}, watchSnap{}
	}
	snap := watchSnap{ModTime: info.ModTime(), Size: info.Size(), Count: 1}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err == nil {
			snap.Count = len(entries)
		}
	}
	return snap, snap
}

// recomputeNext 依据任务类型计算下次触发时间。
func (s *Scheduler) recomputeNext(j *Job, now time.Time) {
	switch {
	case j.Cron != "":
		if c, err := ParseCron(j.Cron); err == nil {
			next := c.Next(now)
			if !next.IsZero() {
				j.NextRun = &next
			}
		}
	case j.IntervalSec > 0:
		next := now.Add(time.Duration(j.IntervalSec) * time.Second)
		j.NextRun = &next
	case j.WatchPath != "":
		next := now.Add(5 * time.Second) // 监听类任务的轮询节拍
		j.NextRun = nil
		_ = next
	}
}

// AddJob 新增任务（校验 Cron/间隔/目标）。
func (s *Scheduler) AddJob(name, cron string, intervalSec int, goal, mode string) (Job, error) {
	return s.AddJobDetailed(name, cron, intervalSec, goal, mode, "", "")
}

// AddJobDetailed 新增任务并记录自然语言时间说法与人读调度说明。
func (s *Scheduler) AddJobDetailed(name, cron string, intervalSec int, goal, mode, whenText, scheduleText string) (Job, error) {
	name = trim(name)
	if name == "" {
		return Job{}, fmt.Errorf("任务名不能为空")
	}
	if !JobNamePattern.MatchString(name) {
		return Job{}, fmt.Errorf("任务名只能使用中文字母数字与 - . _ 空格（最长 64 字），不能含 / 等路径字符")
	}
	if goal == "" {
		return Job{}, fmt.Errorf("目标不能为空")
	}
	if cron == "" && intervalSec <= 0 {
		return Job{}, fmt.Errorf("需要提供 cron 表达式或 interval_sec")
	}
	if cron != "" {
		if _, err := ParseCron(cron); err != nil {
			return Job{}, err
		}
	}
	if intervalSec > 0 && intervalSec < 5 {
		return Job{}, fmt.Errorf("间隔最小为 5 秒")
	}
	if mode != "" && mode != "auto" && mode != "plan_first" && mode != "interactive" {
		return Job{}, fmt.Errorf("非法执行模式 %q", mode)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 任务名是主键：静默覆盖会让用户"新建"变成"改掉旧任务"，且原配置悄无声息丢失。
	if _, exists := s.jobs[name]; exists {
		return Job{}, fmt.Errorf("任务名 %q 已存在，请换一个", name)
	}
	job := &Job{
		Name: name, Cron: cron, IntervalSec: intervalSec, Goal: goal, Mode: mode,
		Enabled: true, WhenText: trim(whenText), ScheduleText: trim(scheduleText),
	}
	s.recomputeNext(job, time.Now())
	s.jobs[name] = job
	if err := s.saveLocked(); err != nil {
		return *job, err
	}
	return *job, nil
}

// DeleteJob 删除任务。
func (s *Scheduler) DeleteJob(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[name]; !ok {
		return false, nil
	}
	delete(s.jobs, name)
	delete(s.watch, name)
	delete(s.lastFire, name)
	return true, s.saveLocked()
}

// SetEnabled 启停任务。
func (s *Scheduler) SetEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[name]
	if !ok {
		return fmt.Errorf("任务 %q 不存在", name)
	}
	j.Enabled = enabled
	if enabled {
		s.recomputeNext(j, time.Now())
	}
	return s.saveLocked()
}

// SetNotify 设置通知策略（always / on_failure / never；空串恢复默认）。
func (s *Scheduler) SetNotify(name, policy string) error {
	norm, err := NormalizeNotify(policy)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[name]
	if !ok {
		return fmt.Errorf("任务 %q 不存在", name)
	}
	if norm == NotifyOnFailure {
		// 默认值不落盘：省掉一个字段，也让"没写过"与"写成了默认"不可区分——
		// 而它们本来就该是同一件事。
		j.Notify = ""
	} else {
		j.Notify = norm
	}
	return s.saveLocked()
}

// TriggerNow 立即触发指定任务（HTTP 回调 / 手动触发入口，设计文档 §4.2.5 事件触发）。
// 返回是否存在且已触发。OneShot 任务触发后自动停用。
func (s *Scheduler) TriggerNow(name string) (bool, error) {
	s.mu.Lock()
	j, ok := s.jobs[name]
	if !ok {
		s.mu.Unlock()
		return false, fmt.Errorf("任务 %q 不存在", name)
	}
	if !j.Enabled {
		s.mu.Unlock()
		return false, fmt.Errorf("任务 %q 已停用", name)
	}
	nowT := time.Now()
	j.LastRun = &nowT
	s.recomputeNext(j, nowT)
	if j.OneShot {
		j.Enabled = false
		j.NextRun = nil
	}
	err := s.saveLocked()
	job := *j
	s.mu.Unlock()
	if s.fire != nil {
		go s.fire(job)
	}
	return true, err
}

// ListJobs 任务列表（按名称排序）。
func (s *Scheduler) ListJobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name < out[k].Name })
	return out
}

// Count 任务数。
func (s *Scheduler) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}

func (s *Scheduler) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].Name < jobs[k].Name })
	data, err := json.MarshalIndent(jobs, "", " ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, data, 0o644)
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
