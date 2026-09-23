// Package std 实现与 Harness 各子系统对接的标准工具：
// reply（回复用户）、memory.save/search、schedule.create/list/delete、skill.list/run。
// 通过小型接口解耦，由运行时装配适配器。
package std

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gleam/internal/nlcron"
	"gleam/internal/textmatch"
	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// ---------- reply ----------

// ReplyTool 直接回复用户的工具（对话型目标的终点）。
type ReplyTool struct{}

func NewReply() *ReplyTool { return &ReplyTool{} }

func (t *ReplyTool) Name() string { return "reply" }
func (t *ReplyTool) Description() string {
	return "直接向用户回复文字。对话、咨询、无需工具的目标以此收尾"
}
func (t *ReplyTool) Permission() types.Permission {
	return types.PermissionReadOnly
}
func (t *ReplyTool) Schema() map[string]any {
	return toolutil.Schema("回复用户", []string{"text"}, map[string]any{
		"text": toolutil.SchemaProp("回复内容（面向用户的完整回答）", "string"),
	})
}
func (t *ReplyTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	text, err := toolutil.RequireStr(args, "text")
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": text}, nil
}

// ---------- memory ----------

// SearchHit 记忆检索结果。
type SearchHit struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
	Score   float32  `json:"score"`
}

// MemoryStore 记忆子系统适配接口。
type MemoryStore interface {
	Remember(content string, tags []string) (string, error)
	Search(query string, k int) []SearchHit
	Count() int
}

type memSaveTool struct{ store MemoryStore }

func NewMemSave(store MemoryStore) types.Tool { return &memSaveTool{store} }

func (t *memSaveTool) Name() string { return "memory.save" }
func (t *memSaveTool) Description() string {
	// 别再往这里加「（用户偏好、项目背景等）」这类**内容举例**：
	// 一是与下面 schema 的 tags 示例重复，二是这些词会被本地关键词打分当成路由信号——
	// 实测「偏好」让它在「检索我之前关于部署的偏好」里压过了 memory.search。
	// 描述只讲「这是什么、什么时候用」，举例留给 schema。
	return "记住重要信息：写入长期记忆，跨会话可用"
}
func (t *memSaveTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *memSaveTool) Schema() map[string]any {
	return toolutil.Schema("保存长期记忆", []string{"content"}, map[string]any{
		"content": toolutil.SchemaProp("要记住的内容", "string"),
		"tags":    toolutil.SchemaProp("标签列表，如 [\"偏好\"]", "array"),
	})
}
func (t *memSaveTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	content, err := toolutil.RequireStr(args, "content")
	if err != nil {
		return nil, err
	}
	var tags []string
	if arr, ok := args["tags"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				tags = append(tags, s)
			}
		}
	}
	id, err := t.store.Remember(content, tags)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "saved": true}, nil
}

type memSearchTool struct{ store MemoryStore }

func NewMemSearch(store MemoryStore) types.Tool { return &memSearchTool{store} }

func (t *memSearchTool) Name() string                 { return "memory.search" }
func (t *memSearchTool) Description() string          { return "按语义相关度检索长期记忆" }
func (t *memSearchTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *memSearchTool) Schema() map[string]any {
	return toolutil.Schema("检索长期记忆", []string{"query"}, map[string]any{
		"query": toolutil.SchemaProp("检索词", "string"),
		"k":     toolutil.SchemaProp("返回条数（默认 5）", "integer"),
	})
}
func (t *memSearchTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	query, err := toolutil.RequireStr(args, "query")
	if err != nil {
		return nil, err
	}
	hits := t.store.Search(query, toolutil.Int(args, "k", 5))
	return map[string]any{"query": query, "count": len(hits), "hits": hits}, nil
}

// Outcome 实现 types.OutcomeReporter：没检索到任何记忆是空结果，不是错误。
func (t *memSearchTool) Outcome(args map[string]any, out any) (types.StepOutcome, string) {
	return toolutil.EmptyIfNone(out)
}

// MemoryDeleter 软删除能力（可选实现：只有真接了 Manager 的适配器才有）。
type MemoryDeleter interface {
	SoftDelete(id string) bool
}

type memDeleteTool struct{ store MemoryStore }

func NewMemDelete(store MemoryStore) types.Tool { return &memDeleteTool{store} }

func (t *memDeleteTool) Name() string { return "memory.delete" }
func (t *memDeleteTool) Description() string {
	return "删除一条长期记忆（按 memory.search 返回的 id）；用户要求忘掉某条信息时使用"
}
func (t *memDeleteTool) Permission() types.Permission {
	// 删除是用户数据的有损操作，不能让模型静默决定——走用户审批
	return types.PermissionUserApproved
}
func (t *memDeleteTool) Schema() map[string]any {
	return toolutil.Schema("删除长期记忆", []string{"id"}, map[string]any{
		"id": toolutil.SchemaProp("要删除的记忆条目 ID（来自 memory.search 的结果）", "string"),
	})
}
func (t *memDeleteTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	id, err := toolutil.RequireStr(args, "id")
	if err != nil {
		return nil, err
	}
	d, ok := t.store.(MemoryDeleter)
	if !ok {
		return nil, fmt.Errorf("当前记忆存储不支持删除")
	}
	if !d.SoftDelete(id) {
		return nil, fmt.Errorf("记忆条目不存在或已删除: %s", id)
	}
	return map[string]any{"id": id, "deleted": true}, nil
}

// ---------- schedule ----------

// JobDef 调度任务定义（与 scheduler.Job 解耦的传输结构）。
type JobDef struct {
	Name         string `json:"name"`
	Cron         string `json:"cron,omitempty"`
	IntervalSec  int    `json:"interval_sec,omitempty"`
	Goal         string `json:"goal"`
	Mode         string `json:"mode,omitempty"`
	Enabled      bool   `json:"enabled"`
	WhenText     string `json:"when_text,omitempty"`     // 用户原始时间说法
	ScheduleText string `json:"schedule_text,omitempty"` // 解析后的人读调度说明
	NextRun      string `json:"next_run,omitempty"`
	LastRun      string `json:"last_run,omitempty"`
}

// ScheduleManager 调度子系统适配接口。
type ScheduleManager interface {
	AddJob(name, cron string, intervalSec int, goal, mode string) (JobDef, error)
	DeleteJob(name string) (bool, error)
	ListJobs() []JobDef
}

// ScheduleManagerDetailed 支持持久化自然语言时间说法（适配器可实现）。
type ScheduleManagerDetailed interface {
	AddJobDetailed(name, cron string, intervalSec int, goal, mode, whenText, scheduleText string) (JobDef, error)
}

type scheduleCreateTool struct{ mgr ScheduleManager }

func NewScheduleCreate(mgr ScheduleManager) types.Tool { return &scheduleCreateTool{mgr} }

func (t *scheduleCreateTool) Name() string { return "schedule.create" }
func (t *scheduleCreateTool) Description() string {
	// 这里承担了「参数怎么写」的全部说明：规划器提示词的稳定段只留一句触发条件，
	// 细节放在工具自己的描述里，选中该工具时才加载（渐进式展开）。
	// 别把这些挪回提示词稳定段——那样每轮规划都要重复一遍。
	return "创建定时/周期任务：到点自动执行一个目标。支持用中文自然语言描述时间（推荐 when），也支持 cron 或固定间隔。" +
		"当用户说“每天/每周/每月/工作日/周末/每隔多久/到点提醒我 + 做某事”时调用此工具。" +
		"name 取简短中文任务名（如“每日下载目录整理”），goal 是到点真正要执行的事，二者都不能为空；" +
		"时间用中文原话放进 when（如“每天早上9点”“工作日18:30”），不要自己拼 cron。" +
		"创建成功后用 reply 向用户确认“已按什么节奏、执行什么事、下次大约什么时候”。"
}
func (t *scheduleCreateTool) Permission() types.Permission { return types.PermissionUserApproved }
func (t *scheduleCreateTool) Schema() map[string]any {
	return toolutil.Schema("创建定时任务", []string{"name", "goal"}, map[string]any{
		"name":         toolutil.SchemaProp("任务名（唯一、简短）", "string"),
		"goal":         toolutil.SchemaProp("到点要执行的目标描述", "string"),
		"when":         toolutil.SchemaProp("自然语言时间，如“每天早上9点”“工作日18:30”“每周一和周五晚8点”“每月1号9点”“每隔30分钟”。与 cron、interval_sec 三选一，优先用 when", "string"),
		"cron":         toolutil.SchemaProp("Cron 表达式（5 段：分 时 日 月 周），与 when/interval_sec 三选一", "string"),
		"interval_sec": toolutil.SchemaProp("间隔秒数（与 when/cron 三选一，最小 5 秒）", "integer"),
		"mode":         toolutil.SchemaProp("执行模式 auto|plan_first|interactive（默认 auto）", "string"),
	})
}
func (t *scheduleCreateTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	name, err := toolutil.RequireStr(args, "name")
	if err != nil {
		return nil, err
	}
	goal, err := toolutil.RequireStr(args, "goal")
	if err != nil {
		return nil, err
	}
	when := toolutil.Str(args, "when")
	cron := toolutil.Str(args, "cron")
	intervalSec := toolutil.Int(args, "interval_sec", 0)
	cron, intervalSec, summary, err := nlcron.Resolve(cron, intervalSec, when)
	if err != nil {
		return nil, err
	}
	mode := toolutil.Str(args, "mode")
	var job JobDef
	if dm, ok := t.mgr.(ScheduleManagerDetailed); ok {
		job, err = dm.AddJobDetailed(name, cron, intervalSec, goal, mode, strings.TrimSpace(when), summary)
	} else {
		job, err = t.mgr.AddJob(name, cron, intervalSec, goal, mode)
	}
	if err != nil {
		return nil, err
	}
	if job.WhenText == "" {
		job.WhenText = strings.TrimSpace(when)
	}
	if job.ScheduleText == "" && summary != "" {
		job.ScheduleText = summary
	}
	return job, nil
}

type scheduleListTool struct{ mgr ScheduleManager }

func NewScheduleList(mgr ScheduleManager) types.Tool { return &scheduleListTool{mgr} }

func (t *scheduleListTool) Name() string                 { return "schedule.list" }
func (t *scheduleListTool) Description() string          { return "列出全部定时任务" }
func (t *scheduleListTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *scheduleListTool) Schema() map[string]any {
	return toolutil.Schema("列出定时任务", nil, map[string]any{})
}
func (t *scheduleListTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	jobs := t.mgr.ListJobs()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	return map[string]any{"count": len(jobs), "jobs": jobs}, nil
}

type scheduleDeleteTool struct{ mgr ScheduleManager }

func NewScheduleDelete(mgr ScheduleManager) types.Tool { return &scheduleDeleteTool{mgr} }

func (t *scheduleDeleteTool) Name() string { return "schedule.delete" }
func (t *scheduleDeleteTool) Description() string {
	return "删除定时任务（高风险：影响后续自动化，需要用户批准）"
}
func (t *scheduleDeleteTool) Permission() types.Permission { return types.PermissionFullAccess }
func (t *scheduleDeleteTool) Schema() map[string]any {
	return toolutil.Schema("删除定时任务", []string{"name"}, map[string]any{
		"name": toolutil.SchemaProp("任务名", "string"),
	})
}
func (t *scheduleDeleteTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	name, err := toolutil.RequireStr(args, "name")
	if err != nil {
		return nil, err
	}
	ok, err := t.mgr.DeleteJob(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("定时任务 %q 不存在", name)
	}
	return map[string]any{"name": name, "deleted": true}, nil
}

// ---------- skill ----------

// SkillSummary 技能摘要。
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
	Runs        int    `json:"runs"`
	Successes   int    `json:"successes"`
}

// SkillStore 技能子系统适配接口。
type SkillStore interface {
	ListSummaries() []SkillSummary
	GetParams(name string) ([]string, error)
}

// SkillRunner 技能执行入口（由 Agent 实现）。
type SkillRunner interface {
	RunSkill(ctx context.Context, name string, params map[string]string) (map[string]any, error)
}

type skillListTool struct{ store SkillStore }

func NewSkillList(store SkillStore) types.Tool { return &skillListTool{store} }

func (t *skillListTool) Name() string { return "skill.list" }
func (t *skillListTool) Description() string {
	return "列出已固化的技能（可复用的工作流）"
}
func (t *skillListTool) Permission() types.Permission { return types.PermissionReadOnly }

// Schema 把「怎么筛」写在**参数说明**里，而不是工具描述里。
//
// 工具描述会进稳定段的能力菜单（一行一个工具），改动一次就是**每个提示词都多付一遍**；
// 参数说明只在 skill.list 被选中时才出现，而"要不要传 query"本来就是看着参数说明决定的。
// 同样的字写在便宜的地方，不是抠字，是别把稳定段当成随手记的地方。
func (t *skillListTool) Schema() map[string]any {
	return toolutil.Schema("列出技能", nil, map[string]any{
		"query": toolutil.SchemaProp("按目标做相关度筛选，只返回最相关的几条；不填则列出全部", "string"),
		"limit": toolutil.SchemaProp("query 筛选时最多返回几条（默认 10，上限 50）", "integer"),
	})
}

// skillQueryLimit / skillQueryMaxLimit 带 query 筛选时的默认与上限返回条数。
//
// 有上限才有意义：筛了却把 200 条全返回，等于没筛——上下文照样被一次 skill.list
// 塞满，只是从"全部技能"变成"按相关度排过序的全部技能"。上限是这条改动的全部价值所在。
const (
	skillQueryLimit    = 10
	skillQueryMaxLimit = 50
)

func (t *skillListTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	all := t.store.ListSummaries()
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	// 不带 query：保持全量（向后兼容）。技能不多时这是最省事的一条路，
	// 而且"我到底有哪些技能"这个问题本来就该给全——那时筛反而是藏信息。
	query := strings.TrimSpace(toolutil.Str(args, "query"))
	if query == "" {
		return map[string]any{"count": len(all), "skills": all}, nil
	}

	limit := toolutil.Int(args, "limit", skillQueryLimit)
	if limit <= 0 {
		limit = skillQueryLimit
	}
	if limit > skillQueryMaxLimit {
		limit = skillQueryMaxLimit
	}

	hits := rankSkills(query, all)
	matched := len(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := map[string]any{"count": len(hits), "total": len(all), "skills": hits}
	if matched == 0 {
		// 刻意**不**用"按名字补齐"把结果填满：菜单一旦永远填满，"在列表里"就不再
		// 意味着"相关"，筛选也就白做了。宁可直接说没有，并给出看全部的路。
		out["note"] = fmt.Sprintf("没有与目标相关的技能（共 %d 个）。不带 query 可列出全部", len(all))
	} else {
		out["note"] = fmt.Sprintf("已按目标筛选：共 %d 个技能，相关 %d 个，返回前 %d 个。要看全部可再调一次不带 query 的 skill.list",
			len(all), matched, len(hits))
	}
	return out, nil
}

// rankSkills 按目标给技能打相关度分并排序：名称命中权重更高。
//
// 判据与工具路由共用 textmatch 一份实现（中文二元组 + 英文整词）。两处各写一份，
// "相关"就会有两个含义——同一句目标在工具列表里排得上、在技能列表里排不上，
// 而两边看起来都正常，这种偏差没人查得出来。
func rankSkills(query string, list []SkillSummary) []SkillSummary {
	terms := textmatch.Terms(query)
	type scored struct {
		s SkillSummary
		n int
	}
	var hits []scored
	for _, s := range list {
		// terms 为空时 Score 恒返回 0 → 不会有命中，不会误报"相关"。
		n := textmatch.Score(terms, s.Description) + 2*textmatch.Score(terms, s.Name)
		if n > 0 {
			hits = append(hits, scored{s, n})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].n != hits[j].n {
			return hits[i].n > hits[j].n
		}
		return hits[i].s.Name < hits[j].s.Name // 同分按名字排序，保证结果稳定
	})
	out := make([]SkillSummary, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.s)
	}
	return out
}

type skillRunTool struct {
	store  SkillStore
	runner SkillRunner
}

func NewSkillRun(store SkillStore, runner SkillRunner) types.Tool {
	return &skillRunTool{store, runner}
}

func (t *skillRunTool) Name() string { return "skill.run" }
func (t *skillRunTool) Description() string {
	return "执行一个已固化的技能（一键复用历史工作流）"
}
func (t *skillRunTool) Permission() types.Permission { return types.PermissionUserApproved }
func (t *skillRunTool) Schema() map[string]any {
	return toolutil.Schema("运行技能", []string{"name"}, map[string]any{
		"name":   toolutil.SchemaProp("技能名", "string"),
		"params": toolutil.SchemaProp("参数映射（技能声明的占位符）", "object"),
	})
}
func (t *skillRunTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	name, err := toolutil.RequireStr(args, "name")
	if err != nil {
		return nil, err
	}
	paramsRaw := toolutil.Map(args, "params")
	params := map[string]string{}
	for k, v := range paramsRaw {
		params[k] = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	if declared, err := t.store.GetParams(name); err == nil {
		for _, p := range declared {
			if _, ok := params[p]; !ok {
				return nil, fmt.Errorf("缺少技能参数 %q（该技能声明了参数：%v）", p, declared)
			}
		}
	}
	return t.runner.RunSkill(ctx, name, params)
}
