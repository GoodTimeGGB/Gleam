// 用户反馈与建议的 HTTP 端点。
//
// 这一层只干三件事：**收下**（校验形状）、**补现场**（上下文由后端生成，不信前端传的）、
// **落盘**（先本地归档，投递是下一步的事）。
//
// 为什么上下文一定在这边组装：反馈是会被发出去的，而前端能填的字段就等于
// 任何人都能往里面写任何东西。让它只在本机从配置里取白名单字段，
// 「发出去的那份里有什么」才是一个能回答的问题。
package webui

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gleam/internal/agent"
	"gleam/internal/buildinfo"
	"gleam/internal/harness/feedback"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

const (
	// maxFeedbackTextRunes 描述正文的上限。反馈会进列表、也可能被整条发出去，
	// 而真正有用的 bug 描述是几句话——超长的那半通常是把日志正文粘进来了。
	maxFeedbackTextRunes = 20000
	// maxFeedbackAttachments 单条反馈的截图张数上限。
	maxFeedbackAttachments = 5
	// feedbackBodyCap 提交请求体的上限：5 张原图 base64 后约 5MB×5×4/3，
	// 留出余量，同时仍是一个"明显不是正常提交"的界线。
	feedbackBodyCap int64 = 40 << 20
	// feedbackDeliverTimeout 远端投递的天花板。本地归档落住之后才走它，
	// 所以这个数只决定"提交"这件事在网络上最多停多久，不决定反馈在不在。
	feedbackDeliverTimeout = 15 * time.Second
)

// feedbackSubmitRequest 提交体。**没有 context 字段**——现场由后端补，见包注释。
type feedbackSubmitRequest struct {
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	TaskID      string `json:"task_id,omitempty"`
	Attachments []struct {
		Data string `json:"data"`
	} `json:"attachments,omitempty"`
}

// handleFeedbackSubmit 收下一条反馈：校验 → 存截图 → 补现场 → 落盘。
//
// 顺序是"截图先、记录后"，且任一步失败就把已经落盘的部分删干净。反过来会留下
// 一批没有主人的截图：反馈 JSON 才是那些文件的名字来源，它没了就没人知道该删谁。
func (s *Server) handleFeedbackSubmit(w http.ResponseWriter, r *http.Request) {
	var req feedbackSubmitRequest
	if err := readJSONCap(r, feedbackBodyCap, &req); err != nil {
		writeErr(w, 400, "参数解析失败或请求过大")
		return
	}
	kind, err := parseFeedbackKind(req.Kind)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeErr(w, 400, "请先写一句描述：没有文字的截图，收到也不知道该改什么")
		return
	}
	if len([]rune(text)) > maxFeedbackTextRunes {
		writeErr(w, 400, "描述过长（%d 字，上限 %d 字）——把要提交的文字放进截图或文件里更合适",
			len([]rune(text)), maxFeedbackTextRunes)
		return
	}
	if len(req.Attachments) > maxFeedbackAttachments {
		writeErr(w, 400, "截图最多 %d 张（当前 %d 张）", maxFeedbackAttachments, len(req.Attachments))
		return
	}

	id := feedback.NewID()
	var stored []types.FeedbackAttachment
	for _, a := range req.Attachments {
		data, err := decodeFeedbackImage(a.Data)
		if err != nil {
			s.discardFeedback(id)
			writeErr(w, 400, "%v", err)
			return
		}
		name, mime, err := s.feedback.SaveAttachment(id, data)
		if err != nil {
			s.discardFeedback(id)
			writeErr(w, 400, "%v", err)
			return
		}
		stored = append(stored, types.FeedbackAttachment{Name: name, Mime: mime, Bytes: len(data)})
	}

	f := &types.Feedback{
		ID:          id,
		Kind:        kind,
		Text:        text,
		Attachments: stored,
		Context:     s.feedbackContext(req.TaskID),
		CreatedAt:   time.Now().UTC(),
		Delivery:    types.FeedbackLocalOnly,
	}
	if err := s.feedback.Save(f); err != nil {
		s.discardFeedback(id)
		writeErr(w, 500, "反馈没能存下来：%v", err)
		return
	}
	// 归档落住之后才谈投递：顺序反过来的话，一次断网会让用户重填一遍，
	// 而那条反馈其实早就在他机器上了。
	s.deliverFeedback(f)
	writeJSON(w, 201, f)
}

// handleFeedbackContext 回显"这条反馈将被带上的运行现场"，供提交前预览。
//
// 为什么开这一个只读端点而不是让前端自己拼：那份白名单的 owner 是 `feedbackContext`，
// 前端抄一份字段名就是第二个事实源——后端改一处，预览立刻变成一句谎报。
// 预览说的是真话，用户才是在**看过之后**决定发不发。
func (s *Server) handleFeedbackContext(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"context": s.feedbackContext(r.URL.Query().Get("task_id")),
		"remote":  s.feedbackRemoteName(),
	})
}

// handleFeedbackList 列出本机已提交的反馈。
func (s *Server) handleFeedbackList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	list, skipped, err := s.feedback.List(limit)
	if err != nil {
		writeErr(w, 500, "反馈列表读不动：%v", err)
		return
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "[gleam] %d 条反馈读不动，已跳过\n", skipped)
	}
	writeJSON(w, 200, map[string]any{
		"count":    len(list),
		"skipped":  skipped,
		"remote":   s.feedbackRemoteName(),
		"feedback": list,
	})
}

// feedbackRemoteName 远端投递器的名字，没配就是空串。
//
// 列表要回答「我的反馈到底去了哪儿」，而这件事的 owner 是 `feedbackSink`：
// 前端另猜一遍（比如「登录了就算配了」）就会在换后端时悄悄说错。
func (s *Server) feedbackRemoteName() string {
	if sink := s.feedbackSink(); sink != nil {
		return sink.Name()
	}
	return ""
}

// handleFeedbackDelete 删掉一条反馈连同它的截图。
func (s *Server) handleFeedbackDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 先读一次再删：Delete 对"形状不合的 id"是空操作（本来就没东西可删），
	// 直接回 deleted 就把"你删的那条不存在"报成了"删好了"。
	f, err := s.feedback.Read(id)
	if err != nil {
		writeErr(w, 500, "反馈读不动：%v", err)
		return
	}
	if f == nil {
		writeErr(w, 404, "没有这条反馈")
		return
	}
	if err := s.feedback.Delete(id); err != nil {
		writeErr(w, 500, "删除失败：%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": id})
}

// handleFeedbackAttachment 送出一张截图的原始字节。
//
// 有了它，历史列表里的截图才**看得回来**；只把名字列在列表里，等于让用户
// 知道自己提交过、却没法复查截到了什么——而复查正是删它的理由。
func (s *Server) handleFeedbackAttachment(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	data, mime, err := s.feedback.ReadAttachment(name)
	if err != nil {
		writeErr(w, 500, "截图读不动：%v", err)
		return
	}
	if data == nil {
		writeErr(w, 404, "没有这张截图")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// handleFeedbackResend 手动把一条没送达的反馈再投一次。
//
// 有了失败状态却没有重试入口，等于给用户一个只能看着的红点：
// 他会以为"反馈"这功能坏了，而实际上只是当时没网。
func (s *Server) handleFeedbackResend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := s.feedback.Read(id)
	if err != nil {
		writeErr(w, 500, "反馈读不动：%v", err)
		return
	}
	if f == nil {
		writeErr(w, 404, "没有这条反馈")
		return
	}
	if s.feedbackSink() == nil {
		writeErr(w, 400, "还没配远端投递：先在「我的 → 账号」填 Supabase 项目地址与 anon key")
		return
	}
	s.deliverFeedback(f)
	writeJSON(w, 200, f)
}

// deliverFeedback 送去一份脱敏副本，并把结果写回本地归档。
//
// 已送达的直接跳过：重投会在表里多出一条重复行，"这条反馈收没收到"就说不清了。
// 失败只改状态、不改提交结果——本地归档此刻已经是这条反馈唯一的凭据。
func (s *Server) deliverFeedback(f *types.Feedback) {
	sink := s.feedbackSink()
	// 没配远端：local_only 就是这条反馈的**最终**状态，不是"还没做完"。
	if sink == nil || f.Delivery == types.FeedbackSent {
		return
	}
	out, redacted := feedback.Redact(f, s.feedbackSecrets())
	ctx, cancel := context.WithTimeout(context.Background(), feedbackDeliverTimeout)
	defer cancel()
	if err := sink.Send(ctx, out); err != nil {
		f.Delivery = types.FeedbackFailed
		f.DeliveryNote = fmt.Sprintf("已存本机，未送达 %s：%s",
			sink.Name(), types.Shorten(err.Error(), 160))
	} else {
		f.Delivery = types.FeedbackSent
		f.DeliveryNote = fmt.Sprintf("已送达 %s", sink.Name())
	}
	if redacted > 0 {
		// 说动了哪儿：用户点的是"提交反馈"，不是"把我的路径发出去"。
		// 被动过这件事要留痕，不然对方收到的那份和他写的那份不一致，而没人知道。
		f.DeliveryNote += fmt.Sprintf("（发出的副本里替换了 %d 处机密片段）", redacted)
	}
	// 状态回写归档：投递结果只活在一次响应里的话，刷新之后"未送达"这个提醒就没了。
	if err := s.feedback.Save(f); err != nil {
		fmt.Fprintf(os.Stderr, "[gleam] 反馈 %s 的投递状态没写回去：%v\n", f.ID, err)
	}
}

// feedbackSink 按当前凭证决定往哪儿投；没配远端返回 nil（那是 local_only，不是失败）。
//
// 每次提交现取而不是启动时定一次：账号配置就是在界面上改的，重启才生效的投递器
// 会让人以为"配了没用"。
func (s *Server) feedbackSink() feedback.Sink {
	if s.Agent.Creds == nil {
		return nil
	}
	c := s.Agent.Creds.GetCloudConfig()
	if c == nil {
		return nil
	}
	target := feedback.SupabaseTarget{URL: c.SupabaseURL, AnonKey: c.SupabaseAnonKey}
	if !target.Configured() {
		return nil
	}
	// 出网留痕接在装配点上：这条路径发出去的是**用户写的那段话**（已脱敏），
	// 台账必须能说出它去了哪个主机、多大，而审计里永远只有主机与字节。
	return feedback.NewSupabase(target, func(host string, nbytes int) {
		s.Agent.Gate.RecordEgress("feedback", host, nbytes)
	})
}

// feedbackSecrets 本机已知**不该外发**的片段（发出去之前从副本里挖掉）。
//
// 为什么是"已知片段"而不是通用规则：只有这些是我们可以确指的东西——当前生效的密钥、
// 工作区与数据目录的绝对路径（带着用户名）。除此之外的正文是用户自己要说的话，
// 猜着打码会把内容打花，而他以为我们收到的是原文。
func (s *Server) feedbackSecrets() []string {
	cfg := s.Agent.Cfg
	out := []string{cfg.Workspace, cfg.DataDir}
	if key, _ := agent.LLMKeyFor(cfg, s.Agent.Creds, cfg.LLM.BaseURL, ""); key != "" {
		out = append(out, key)
	}
	if cfg.LLM.APIKey != "" {
		// 内存里生效的那把可能没绑当前主机（换厂商之后还没重填），它同样不该出去。
		out = append(out, cfg.LLM.APIKey)
	}
	return out
}

// feedbackContext 组装一条反馈的运行现场。
//
// **逐字段点名，绝不整块拷贝配置**：配置里有 api_key（哪怕已加密也不该出现在这里）、
// 工作区绝对路径（会带上用户名）和用户的自定义指令。少抄一处就少一处泄漏面，
// 而"以后要加字段"永远比"现在漏了"好改。
// taskID 非空时（界面上"反馈这条"）按它取那次运行的终态；为空取最近一次。
func (s *Server) feedbackContext(taskID string) types.FeedbackContext {
	cfg := s.Agent.Cfg
	ctx := types.FeedbackContext{
		AppVersion: buildinfo.Version,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS + "/" + runtime.GOARCH,
		Model:      strings.TrimSpace(cfg.LLM.Model),
		// 只留主机名：完整 base_url 的路径段在某些厂商那儿就是密钥的一部分。
		LLMHost: llm.KeyScope(cfg.LLM.BaseURL),
	}
	res := s.recentTaskResult(taskID)
	if res == nil {
		return ctx
	}
	ctx.TaskID = res.TaskID
	ctx.TaskStatus = res.Status
	// 倒着找第一条失败的步骤：一步可能重试多次，**最后一次**失败才是用户看到的那个错。
	for i := len(res.Steps) - 1; i >= 0; i-- {
		if res.Steps[i].Status == types.StepFailed && res.Steps[i].Tool != "" {
			ctx.FailedTool = res.Steps[i].Tool
			break
		}
	}
	return ctx
}

// recentTaskResult 取"这次反馈要说的那次运行"：内存表优先，回落盘上的终态归档。
// 优先级与 GET /api/goals/{id} 一致——跑着呢的任务是 result==nil，拿档案答它就把
// "跑着呢"报成"已结束"。taskID 为空时取最近一次（重启后内存表是空的，归档还在盘上）。
func (s *Server) recentTaskResult(taskID string) *types.GoalResult {
	if taskID != "" {
		if status, res := s.Agent.TaskStatus(taskID); res != nil {
			return res
		} else if status != "" {
			return &types.GoalResult{TaskID: taskID, Status: status}
		}
		if t, ok := s.archivedTask(taskID); ok {
			return t.Result
		}
		// 两头都没有也要把 task_id 带上：用户点的是这条卡片，反馈就该指着它，
		// 而不是因为查不到就悄悄换成"最近一次"——那是另一件事。
		return &types.GoalResult{TaskID: taskID}
	}
	if res := s.newestMemoryTask(); res != nil {
		return res
	}
	// 只要最新一条，所以不借道任务列表：那一次调用会把 100 条归档全解析出来。
	archived, _, err := agent.ListTaskResults(s.Agent.Cfg.DataDir, 1)
	if err != nil || len(archived) == 0 {
		return nil
	}
	return archived[0]
}

// newestMemoryTask 内存任务表里最近启动的那次运行（可能还在跑）。
func (s *Server) newestMemoryTask() *types.GoalResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	var newest *taskInfo
	for _, t := range s.tasks {
		if newest == nil || t.Started.After(newest.Started) {
			newest = t
		}
	}
	if newest == nil {
		return nil
	}
	if newest.Result != nil {
		return newest.Result
	}
	return &types.GoalResult{TaskID: newest.ID, Status: newest.Status}
}

// discardFeedback 回滚一次没提交成的反馈：截图先落了盘，记录没写成，就得连截图一起撤。
func (s *Server) discardFeedback(id string) {
	if err := s.feedback.Delete(id); err != nil {
		fmt.Fprintf(os.Stderr, "[gleam] 反馈 %s 回滚失败，feedback/ 里可能残留它的截图：%v\n", id, err)
	}
}

func parseFeedbackKind(raw string) (types.FeedbackKind, error) {
	switch strings.TrimSpace(raw) {
	case string(types.FeedbackBug):
		return types.FeedbackBug, nil
	case string(types.FeedbackSuggestion):
		return types.FeedbackSuggestion, nil
	}
	return "", fmt.Errorf("反馈类型只能是「问题」或「建议」（当前 %q）", raw)
}

// decodeFeedbackImage 解前端传来的图片。容忍 data URL 前缀，因为剪贴板粘贴拿到的就是它。
func decodeFeedbackImage(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "base64,"); i >= 0 {
		s = s[i+len("base64,"):]
	}
	if s == "" {
		return nil, fmt.Errorf("截图是空的")
	}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		// 粘贴路径偶尔丢 padding，换 RawStdEncoding 再试一次；再不动就是真的不是 base64。
		data, err = base64.RawStdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
		if err != nil {
			return nil, fmt.Errorf("截图没认出来（不是 base64）")
		}
	}
	return data, nil
}
