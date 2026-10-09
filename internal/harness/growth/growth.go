// Package growth 实现 Gleam 的成长日志系统。
//
// 记录任务执行历史、技能固化与使用统计、效率指标，
// 形成"养成系"数据——让用户看到 Gleam 的成长轨迹。
package growth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gleam/internal/atomicfile"
)

// Log 成长日志核心。
type Log struct {
	mu      sync.Mutex
	path    string
	entries []Entry
}

// Entry 单条成长记录。
type Entry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Type      string    `json:"type"` // task_completed | skill_created | skill_used | milestone | efficiency
	Goal      string    `json:"goal,omitempty"`
	Score     int       `json:"score,omitempty"`
	Steps     int       `json:"steps,omitempty"`
	Duration  float64   `json:"duration_seconds,omitempty"` // 耗时（秒）
	Tokens    int       `json:"tokens,omitempty"`           // 本次任务的模型 token 总量
	LLMCalls  int       `json:"llm_calls,omitempty"`        // 模型调用次数
	ToolCalls int       `json:"tool_calls,omitempty"`       // 工具调用次数
	SkillName string    `json:"skill_name,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	// RuleSet 这次任务所用规则集的版本指纹（对话模式为空）。
	// 记下来是为了回溯：换了规则之后要能回答"这批历史任务是哪一版跑出来的"，
	// 否则历史数据只能当成一个不可比的混合体。
	RuleSet string `json:"rule_set,omitempty"`
	// Retried 用户重试后完成的任务（同一会话短时间相似目标再次提交，P3-4）。
	// 任务"完成"和"用户满意"是两回事——重试完成的任务是质量的诚实折扣。
	Retried bool `json:"retried,omitempty"`
	// Delivery 这条记录是否带**交付侧口径**（FirstPass / Reworks）。
	// 只有走过工作循环的任务才有"产物是不是一次就合格"这回事，对话模式没有产物可核。
	//
	// 单独记一个布尔，而不是靠"FirstPass 是不是零值"去猜：零值既可能是
	// "没通过首次验收"，也可能是"根本没这个口径"——两者混在一起，
	// 统计的分母就不可信，而分母不可信的比率比没有比率更糟。
	Delivery bool `json:"delivery,omitempty"`
	// FirstPass 第一轮尝试就走完流程并通过完成判定（验收 + 产物核对）。
	// 它不等于"成功"：返工一次才成同样是成功，但不是一次就合格。
	FirstPass bool `json:"first_pass,omitempty"`
	// Reworks 反思判未达标后真正重新执行的轮数（规划校验失败的重规划不计）。
	Reworks int `json:"reworks,omitempty"`
}

// Stats 成长统计。
type Stats struct {
	TotalTasks    int            `json:"total_tasks"`
	TotalSkills   int            `json:"total_skills"`
	SkillUses     int            `json:"skill_uses"`
	AvgScore      float64        `json:"avg_score"`
	TotalDuration float64        `json:"total_duration_seconds"`
	TotalTokens   int            `json:"total_tokens"`        // 累计 token
	WeekTokens    int            `json:"week_tokens"`         // 近 7 天 token
	TotalLLMCalls int            `json:"total_llm_calls"`     // 累计模型调用次数
	AvgTokens     float64        `json:"avg_tokens_per_task"` // 单任务平均 token
	TasksByType   map[string]int `json:"tasks_by_type"`
	RecentStreak  int            `json:"recent_streak"` // 连续活跃天数
	// 用户重试率 / 中断率（P3-4，站点 H15：最接近真相的质量指标）。
	// 重试 = 用户觉得不行又发了一遍；中断 = 用户不等了。两者都是用户替你打的分，
	// 且重试的原始 trace 天然是 badcase 池（gleam eval --emit-case 直接回流）。
	RetryCount   int     `json:"retry_count"`   // 重试完成的任务数
	AbortedTasks int     `json:"aborted_tasks"` // 用户中断的任务数
	RetryRate    float64 `json:"retry_rate"`    // 重试率 = RetryCount / (completed + aborted)
	AbortRate    float64 `json:"abort_rate"`    // 中断率 = AbortedTasks / (completed + aborted)
	// 交付侧一次性（P5）：产物是不是一次就合格。
	//
	// 与上面两个"用户替你打分"的指标互补：重试率/中断率回答"用户满不满意"，
	// 这一组回答"系统一次做对的比例有多高"。返工一次才成与一次做对，
	// 在成功率上完全一样，在成本与可信度上完全是两回事——成功率看不见这个差别。
	//
	// **分母只算记过这个口径的完成任务**（Delivery 为真的那些，即工作模式）：
	// 对话模式没有产物可核，历史条目也从没记过。把没记过的算成"没通过首次验收"，
	// 会让比率随使用习惯和历史长度漂移，而不是随交付质量漂移——
	// 与 RetryRate 把 skill 条目排除在分母外是同一个道理。
	FirstPassCount int     `json:"first_pass_count"` // 一次就合格的完成任务数
	FirstPassBase  int     `json:"first_pass_base"`  // 分母：记过交付口径的完成任务数
	FirstPassRate  float64 `json:"first_pass_rate"`  // = FirstPassCount / FirstPassBase
	// ReworksTotal 累计返工轮数。**含中断任务中断前已发生的返工**——
	// 用户放弃之前烧掉的那几轮同样花了钱，只统计"最后成功了"的任务会把浪费藏起来。
	ReworksTotal  int     `json:"reworks_total"`
	Level         string  `json:"level"`          // 等级
	LevelProgress float64 `json:"level_progress"` // 当前等级进度 0-1
}

// Open 打开或创建成长日志。
func Open(dataDir string) (*Log, error) {
	path := filepath.Join(dataDir, "growth.json")
	l := &Log{path: path}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	l.entries = entries
	return l, nil
}

// Record 记录一条成长事件。
func (l *Log) Record(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.ID == "" {
		e.ID = fmtEntryID(l.entries)
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.entries = append(l.entries, e)
	l.persist()
}

// persist 持久化到磁盘。整份条目表只有一个文件，写成半截就等于丢掉全部成长记录，
// 所以走原子替换；写失败在此处只能吞掉（成长是旁路，不该让任务失败），
// 由 doctor 的"成长日志不可读"检查在最近处兜住。
func (l *Log) persist() {
	data, err := json.MarshalIndent(l.entries, "", "  ")
	if err != nil {
		return
	}
	_ = atomicfile.Write(l.path, data, 0o644)
}

// Recent 返回最近的 n 条记录。
func (l *Log) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.entries) {
		n = len(l.entries)
	}
	start := len(l.entries) - n
	if start < 0 {
		start = 0
	}
	// 返回倒序（最新在前）
	result := make([]Entry, n)
	for i := 0; i < n; i++ {
		result[i] = l.entries[start+n-1-i]
	}
	return result
}

// Stats 统计当前成长指标。
func (l *Log) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()

	s := Stats{TasksByType: map[string]int{}}
	var totalScore float64
	scoredTasks := 0
	weekAgo := time.Now().AddDate(0, 0, -7)

	for _, e := range l.entries {
		switch e.Type {
		case "task_completed":
			s.TotalTasks++
			s.TasksByType["task"]++
			if e.Retried {
				s.RetryCount++
			}
			// 交付侧口径：只算记过的（Delivery），否则分母会被对话任务和
			// 历史条目稀释——那两类的 FirstPass 恒为 false，会把比率压成噪音。
			if e.Delivery {
				s.FirstPassBase++
				if e.FirstPass {
					s.FirstPassCount++
				}
			}
			s.ReworksTotal += e.Reworks
			if e.Score > 0 {
				totalScore += float64(e.Score)
				scoredTasks++
			}
			s.TotalDuration += e.Duration
			s.TotalTokens += e.Tokens
			s.TotalLLMCalls += e.LLMCalls
			if e.Time.After(weekAgo) {
				s.WeekTokens += e.Tokens
			}
		case "task_aborted":
			s.AbortedTasks++
			// 中断前已经返工过的轮数照样算进总量：那是真花掉的成本。
			s.ReworksTotal += e.Reworks
		case "skill_created":
			s.TotalSkills++
			s.TasksByType["skill_created"]++
		case "skill_used":
			s.SkillUses++
			s.TasksByType["skill_used"]++
		}
	}

	// 重试率 / 中断率：分母 = 用户真正提交过的任务（完成 + 中断），
	// 边界必须写清——把分母说成"全部条目"会让比率随日志类型数量漂移。
	if submitted := s.TotalTasks + s.AbortedTasks; submitted > 0 {
		s.RetryRate = float64(s.RetryCount) / float64(submitted)
		s.AbortRate = float64(s.AbortedTasks) / float64(submitted)
	}

	// 首次验收通过率：分母与上面两个不同（只算记过口径的完成任务），
	// 所以**不能**跟它们共用一个分母变量——共用就是口径混账的开始。
	if s.FirstPassBase > 0 {
		s.FirstPassRate = float64(s.FirstPassCount) / float64(s.FirstPassBase)
	}

	if scoredTasks > 0 {
		s.AvgScore = totalScore / float64(scoredTasks)
		s.AvgTokens = float64(s.TotalTokens) / float64(scoredTasks)
	}

	// 连续活跃天数
	s.RecentStreak = l.computeStreak()

	// 等级：基于完成任务数 + 技能数
	score := s.TotalTasks*10 + s.TotalSkills*20 + s.SkillUses*5
	s.Level, s.LevelProgress = computeLevel(score)

	return s
}

func (l *Log) computeStreak() int {
	if len(l.entries) == 0 {
		return 0
	}
	// 取最后一天记录的日期
	last := l.entries[len(l.entries)-1].Time
	today := time.Now()
	days := int(today.Sub(last.Truncate(24*time.Hour)).Hours() / 24)
	if days > 1 {
		return 0 // 超过1天未活动
	}
	// 从最近一天往前数连续天数
	seen := map[string]bool{}
	for _, e := range l.entries {
		seen[e.Time.Format("2006-01-02")] = true
	}
	streak := 0
	d := today
	for {
		key := d.Format("2006-01-02")
		if !seen[key] {
			break
		}
		streak++
		d = d.AddDate(0, 0, -1)
	}
	return streak
}

// computeLevel 基于积分计算等级与进度。
func computeLevel(score int) (string, float64) {
	levels := []struct {
		name string
		min  int
	}{
		{"萌新微光", 0},
		{"初识微光", 50},
		{"渐入佳境", 150},
		{"独当一面", 350},
		{"行家里手", 700},
		{"微光大师", 1200},
		{"光华璀璨", 2000},
	}
	for i := len(levels) - 1; i >= 0; i-- {
		if score >= levels[i].min {
			progress := 0.0
			if i < len(levels)-1 {
				next := levels[i+1].min
				progress = float64(score-levels[i].min) / float64(next-levels[i].min)
			} else {
				progress = 1.0
			}
			return levels[i].name, progress
		}
	}
	return levels[0].name, 0
}

// fmtEntryID 生成简单递增 ID。
func fmtEntryID(existing []Entry) string {
	max := 0
	for _, e := range existing {
		if len(e.ID) > 1 && e.ID[0] == 'g' {
			n := 0
			for _, c := range e.ID[1:] {
				if c >= '0' && c <= '9' {
					n = n*10 + int(c-'0')
				} else {
					break
				}
			}
			if n > max {
				max = n
			}
		}
	}
	return fmtID(max + 1)
}

func fmtID(n int) string {
	// 简单实现避免引入 strconv
	s := ""
	if n == 0 {
		return "g0"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return "g" + s
}

// All 返回全部记录（按时间正序）。
func (l *Log) All() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}
