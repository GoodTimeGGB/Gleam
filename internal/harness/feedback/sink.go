// 投递这一半：**本地归档成功了，才轮到把一份送出去**。
//
// 所以这里没有"必须送达"的语义：Send 的返回值只决定归档上的 `delivery` 是
// sent 还是 failed，永远不决定这条反馈在不在。
package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gleam/pkg/types"
)

// Sink 把一条反馈再送一份到远端。
//
// 为什么是一层接口而不是直接把 Supabase 调用写在 handler 里：远端落点还没定死
// （飞书表格也在候选里），而**已经定**的那半——本地必成、投递可失败可缺席、
// 三态要把"没配"和"没送到"分开说——不该跟着换后端一起动。
// 换后端只换实现，投递顺序与状态口径都不改，这才是可插拔的实际含义。
type Sink interface {
	// Send 送出一份**已脱敏**的副本（脱敏由调用方做，见 Redact）。
	// 返回错误就是"没送到"，调用方负责把 failed 与原因写回归档。
	Send(ctx context.Context, f *types.Feedback) error
	// Name 远端的名字，写进投递备注：失败时要能说出"是没送进 supabase"，
	// 而不是含糊一句"投递失败"。
	Name() string
}

// SupabaseTarget 一个 Supabase 项目的连接信息（anon key 是公开客户端标识，非机密）。
//
// 由**调用方**从凭证里取好再传进来，而不是让 sink 自己去读 credentials 文件：
// "配没配远端"是宿主的问题（它还得据此决定 local_only），
// sink 只管"往这儿送"。少一处反向依赖，也少一份"谁该报未配置"的分歧。
type SupabaseTarget struct {
	URL     string
	AnonKey string
	// Table 表名，留空用 FeedbackTable。
	Table string
}

// Configured 两件都填了才算配好：只有地址没 key 的请求一定 401，
// 把它送出去只会得到一条看不出原因的用户反馈。
func (t SupabaseTarget) Configured() bool {
	return strings.TrimSpace(t.URL) != "" && strings.TrimSpace(t.AnonKey) != ""
}

// table 表名，空则回默认。
func (t SupabaseTarget) table() string {
	if s := strings.TrimSpace(t.Table); s != "" {
		return s
	}
	return FeedbackTable
}

// FeedbackTable 默认的反馈表名。建表 SQL 在 README 的反馈一节（那是它的 owner）。
const FeedbackTable = "feedback_reports"

// supabaseSink 通过 PostgREST 往一张表里插一行。
type supabaseSink struct {
	target SupabaseTarget
	client *http.Client
	egress EgressFunc
}

// EgressFunc 出网留痕回调：每次向远端发包时调用一次，参数只有**主机与字节量**。
//
// 与 llm.EgressFunc、auth 的 OnEgress 同一条边界：审计不能变成新的泄露面，
// 所以签名里就没有能装正文的位置。
type EgressFunc func(host string, nbytes int)

// NewSupabase 造一个 Supabase 投递器。超时是**请求级**的：一次提交里本地归档
// 已经落住了，远端慢不能让界面一直停在"提交中"。
//
// egress 传 nil 就是不记（测试与本地-only 场景）；生产路径由宿主接上安全门控——
// 「这条反馈发去了哪个主机、多大」必须能在台账上指得出留痕。
func NewSupabase(target SupabaseTarget, egress EgressFunc) Sink {
	return &supabaseSink{target: target, client: &http.Client{Timeout: 12 * time.Second}, egress: egress}
}

func (s *supabaseSink) Name() string { return "supabase" }

// Send 插入一行。表不存在（404 / 42P01）单独说清：那是**要人去建表**，
// 不是网络抖动，报"投递失败"会让人反复点重试。
func (s *supabaseSink) Send(ctx context.Context, f *types.Feedback) error {
	body, err := json.Marshal(feedbackRow(f))
	if err != nil {
		return fmt.Errorf("整理投递内容失败: %w", err)
	}
	url := strings.TrimRight(s.target.URL, "/") + "/rest/v1/" + s.target.table()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("apikey", s.target.AnonKey)
	req.Header.Set("Authorization", "Bearer "+s.target.AnonKey)
	req.Header.Set("Content-Type", "application/json")
	// return=minimal：只要"插进去了"，不要把整行回读一遍（回读等于把正文再传一次）。
	req.Header.Set("Prefer", "return=minimal")
	// 留痕在发包**之前**：请求已经发出去了却没能记上（超时、连接被断），
	// 台账就会说"这一类从没出过网"——而在审计里"少记一笔"与"没发生过"长得一样。
	// 记早了的代价只是"发了一次没成功"，那本来就是发生过一次发包。
	if s.egress != nil && req.URL != nil {
		s.egress(req.URL.Host, len(body))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 %s：%v", s.target.table(), err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if resp.StatusCode == http.StatusNotFound || strings.Contains(msg, "42P01") || strings.Contains(msg, "does not exist") {
			return fmt.Errorf("远端没有表 %q：请先在 Supabase 建表（建表语句见 README「反馈与建议」一节）", s.target.table())
		}
		return fmt.Errorf("%d %s", resp.StatusCode, types.Shorten(msg, 200))
	}
	return nil
}

// feedbackRow 一条反馈 → 表里的一行。
//
// **摊平**而不是塞一个嵌套 JSON：会去这张表看东西的人用的是表格视图，
// 摊平的列能直接筛（按版本、按失败工具、按类型），嵌套的那层不能。
// 截图**不外发**，只发文件名/类型/字节数——截到了什么不由我们事先决定，
// 而"为了省事把图一起发出去"这条线一旦开了就没有了。
// 正文不在此处截断：它的上限由提交入口判（见 webui 的 maxFeedbackTextRunes），
// 一处判一次，别让投递层再猜一遍是多少。
func feedbackRow(f *types.Feedback) map[string]any {
	return map[string]any{
		"id":          f.ID,
		"kind":        string(f.Kind),
		"text":        f.Text,
		"app_version": f.Context.AppVersion,
		"go_version":  f.Context.GoVersion,
		"os":          f.Context.OS,
		"model":       f.Context.Model,
		"llm_host":    f.Context.LLMHost,
		"task_id":     f.Context.TaskID,
		"task_status": string(f.Context.TaskStatus),
		"failed_tool": f.Context.FailedTool,
		"attachments": f.Attachments,
		"created_at":  f.CreatedAt.Format(time.RFC3339),
	}
}
