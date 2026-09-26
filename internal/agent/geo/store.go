package geo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gleam/internal/atomicfile"
	"gleam/pkg/types"
)

// maxRecords 历史记录上限（超出淘汰最旧），防止长期运行文件无限增长。
const maxRecords = 200

// Record 一次 GEO 分析的留档，供「GEO」板块回看。
type Record struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id,omitempty"`
	Goal        string    `json:"goal,omitempty"`
	Score       int       `json:"score"`
	Summary     string    `json:"summary"`
	Strengths   []string  `json:"strengths"`
	Weaknesses  []string  `json:"weaknesses"`
	Actionables []Action  `json:"actionables"`
	Source      string    `json:"source"` // auto（创作产出自动分析）| manual（板块手动分析）
	CreatedAt   time.Time `json:"created_at"`
}

// Store GEO 历史存储。零值不可用，请用 Open 构造。
type Store struct {
	path    string
	mu      sync.Mutex
	records []Record
}

// Open 打开（必要时创建）历史文件；读取失败时返回空存储而非报错，
// 避免一个损坏的历史文件拖垮整个引擎装配。
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return s, nil
	}
	var recs []Record
	if err := json.Unmarshal(data, &recs); err != nil {
		return s, nil // 历史文件损坏：丢弃重建
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].CreatedAt.Before(recs[j].CreatedAt) })
	s.records = recs
	return s, nil
}

// Add 追加一条记录并落盘，返回落库后的完整记录（含生成的 ID 与时间）。
func (s *Store) Add(r Record) Record {
	if s == nil {
		return r
	}
	if r.ID == "" {
		r.ID = types.NewID()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	if r.Source == "" {
		r.Source = "auto"
	}
	s.mu.Lock()
	s.records = append(s.records, r)
	if len(s.records) > maxRecords {
		s.records = s.records[len(s.records)-maxRecords:]
	}
	snapshot := make([]Record, len(s.records))
	copy(snapshot, s.records)
	s.mu.Unlock()

	_ = s.persist(snapshot)
	return r
}

// List 返回最近的 n 条记录（最新的在前）；n <= 0 时返回全部。
func (s *Store) List(n int) []Record {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	// 逆序：最新在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Clear 清空历史。
func (s *Store) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.records = nil
	s.mu.Unlock()
	_ = s.persist(nil)
}

// Stats 汇总统计：分析次数、平均分、最高分。
func (s *Store) Stats() (total int, avg float64, best int) {
	if s == nil {
		return 0, 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	total = len(s.records)
	if total == 0 {
		return 0, 0, 0
	}
	sum := 0
	for _, r := range s.records {
		sum += r.Score
		if r.Score > best {
			best = r.Score
		}
	}
	return total, float64(sum) / float64(total), best
}

// persist 落盘快照（在锁外编码，避免持锁做 IO）。
func (s *Store) persist(snapshot []Record) error {
	if s == nil || s.path == "" {
		return nil
	}
	if snapshot == nil {
		snapshot = []Record{}
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, data, 0o644)
}

// ---------- 创作意图识别 ----------

// creativeStrong 强创作信号：命中即视为创作（即使角色不是创作者）。
var creativeStrong = []string{
	"文章", "文案", "博客", "公众号", "新闻稿", "演讲稿", "发言稿", "宣传", "软文",
	"小红书", "微博", "推文", "广告语", "slogan", "标题", "故事", "剧本", "诗",
}

// creativeWeak 一般创作信号：写作类动词与产出物。
var creativeWeak = []string{
	"写一篇", "写一份", "写个", "撰写", "起草", "拟写", "润色", "改写", "扩写", "缩写",
	"生成报告", "总结报告", "报告", "方案", "大纲", "邮件", "信", "简介", "介绍文案",
	"产品介绍", "说明书", "公告", "通知", "文案", "翻译", "论文", "摘要", "旁白",
}

// nonCreative 反向信号：明显是工程/运维/数据类任务时不触发 GEO。
var nonCreative = []string{
	"代码", "函数", "编译", "构建", "单元测试", "重构", "bug", "报错", "日志",
	"部署", "服务器", "数据库", "sql", "脚本执行", "表格", "excel", "csv",
	"安装", "配置环境", "性能调优",
}

// IsCreative 判断一次任务是否属于「创作」，用于决定是否给出 GEO 建议。
// role 非空时，writer 直接判定为创作；其余角色按目标文本关键词判定。
func IsCreative(goal, role string) bool {
	if role == "writer" {
		return true
	}
	goal = strings.ToLower(strings.TrimSpace(goal))
	if goal == "" {
		return false
	}
	strong := containsAny(goal, creativeStrong)
	// 工程/运维角色：只有强创作信号才触发，避免写代码也被拉去做 GEO
	if role == "coder" || role == "ops" || role == "analyst" {
		return strong
	}
	if strong {
		return true
	}
	// 反向信号压过弱信号：例如「把这份报告的数据整理成 Excel」
	if containsAny(goal, nonCreative) && !containsAny(goal, []string{"文案", "文章", "博客"}) {
		return false
	}
	return containsAny(goal, creativeWeak)
}

func containsAny(s string, xs []string) bool {
	for _, x := range xs {
		if x != "" && strings.Contains(s, strings.ToLower(x)) {
			return true
		}
	}
	return false
}
