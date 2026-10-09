package webui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gleam/internal/buildinfo"
	"gleam/internal/harness/credentials"
	"gleam/internal/harness/feedback"
	"gleam/pkg/types"
)

// ---------- 反馈端点（批次 F10）----------
//
// 全部走**真实路由**：这些判据里最可能出错的一类恰恰是"方法写对了、线没接"
// （本仓库栽过多次），直接调方法测不出来。

// pngBytes 一个以 PNG magic 开头的字节串。类型判定只看文件头（不信任声称的 MIME），
// 所以这里不需要一张真图——需要的是"文件头说是 png"这件事。
var pngBytes = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0x20}, 32)...)

// fbDo 发一次请求，返回状态码与解码后的体（体不是 JSON 时 out 为 nil、raw 给原文）。
//
// 不用 fixture 的 call：它在 >=400 时直接 t.Fatal，而这一组测试有一半在验 4xx。
func fbDo(t *testing.T, ts *httptest.Server, method, path string, body any) (int, map[string]any, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, raw
}

func feedbackDir(dataDir string) string { return filepath.Join(dataDir, "feedback") }

// TestFeedbackSubmit_ArchivesLocally 提交的第一件事是**在本机落住**。
//
// 断言盘上有这个文件，而不是只看 201：界面拿到 201 会显示"已提交"，
// 而那句话唯一的凭据就是这份归档。
func TestFeedbackSubmit_ArchivesLocally(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	code, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind": "bug", "text": "点击技能卡片的安装按钮没有反应",
	})
	if code != 201 {
		t.Fatalf("状态 = %d, want 201", code)
	}
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("没有返回 id：%v", got)
	}
	if got["delivery"] != string(types.FeedbackLocalOnly) {
		t.Errorf("没配远端时投递状态应为 %q（不是 failed：那会让人以为提交失败），实得 %v",
			types.FeedbackLocalOnly, got["delivery"])
	}
	b, err := os.ReadFile(filepath.Join(feedbackDir(dataDir), id+".json"))
	if err != nil {
		t.Fatalf("盘上没有归档：%v", err)
	}
	var onDisk types.Feedback
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Text == "" || onDisk.Kind != types.FeedbackBug {
		t.Errorf("归档内容不对：%+v", onDisk)
	}

	// 列表读回来的是同一份，不是又一次提交
	code, list, _ := fbDo(t, ts, "GET", "/api/feedback", nil)
	if code != 200 {
		t.Fatalf("列表状态 = %d", code)
	}
	if n, _ := list["count"].(float64); int(n) != 1 {
		t.Errorf("列表应有 1 条，实得 %v", list["count"])
	}
}

// TestFeedbackList_SaysWhereItGoes 列表必须自己回答「这条反馈去了哪儿」。
//
// 用户最初的问题就是"反馈到哪里我还没想好"，所以界面不能只列条目、让他猜远端
// 算不算配上。`remote` 由 feedbackSink 现算（不是登录状态、不是配置字段直读）：
// 换后端时这里跟着变，前端不需要再改一处。
func TestFeedbackList_SaysWhereItGoes(t *testing.T) {
	srv, _ := newArchiveFixture(t)
	ts := newTokenTestServer(srv)
	defer ts.Close()
	_, list, _ := fbDo(t, ts, "GET", "/api/feedback", nil)
	if r, _ := list["remote"].(string); r != "" {
		t.Errorf("没配远端时 remote 应为空串（界面据此显示「只存本机」），实得 %q", r)
	}

	counts, _ := countHandler()
	dsrv, _ := newDeliveryFixture(t, counts)
	dts := newTokenTestServer(dsrv)
	defer dts.Close()
	_, dl, _ := fbDo(t, dts, "GET", "/api/feedback", nil)
	if r, _ := dl["remote"].(string); r != "supabase" {
		t.Errorf("配了远端时 remote 应为投递器名，实得 %q", r)
	}
}

// TestFeedbackSubmit_ContextIsServerSideAndHostOnly 上下文由**后端**补，且只留主机名。
//
// 两件事一起测，因为它们是同一条判据的两半：
// ① 前端传来的 context 一律不信（反馈是会被发出去的，能填的字段就等于任何人都能写）；
// ② base_url 只留主机名——某些厂商把 token 放在路径段里，完整 URL 一旦离开本机，
//
//	泄漏的就不是"用的哪家模型"而是"用什么凭证用的"。
//	把 `llm.KeyScope(cfg.LLM.BaseURL)` 改成 `cfg.LLM.BaseURL` 就会红。
func TestFeedbackSubmit_ContextIsServerSideAndHostOnly(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	const leakyToken = "sk-leak-in-path-4f9a"
	srv.Agent.Cfg.LLM.Model = "glm-4-plus"
	srv.Agent.Cfg.LLM.BaseURL = "https://gw.example.com/v1/chat/" + leakyToken
	store, err := credentials.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	const apiKey = "sk-real-key-7b3c9d2e"
	if err := store.SetLLMAPIKey(apiKey, "gw.example.com"); err != nil {
		t.Fatal(err)
	}
	ts := newTokenTestServer(srv)
	defer ts.Close()

	code, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind":    "suggestion",
		"text":    "希望技能能批量启用",
		"context": map[string]any{"app_version": "9.9.9", "llm_host": "attacker.example.com", "api_key": apiKey},
	})
	if code != 201 {
		t.Fatalf("状态 = %d", code)
	}
	ctxv, _ := got["context"].(map[string]any)
	if ctxv == nil {
		t.Fatalf("没有 context：%v", got)
	}
	if ctxv["app_version"] != buildinfo.Version {
		t.Errorf("app_version 应为后端真实版本 %q，实得 %v（前端的 context 被采信了）", buildinfo.Version, ctxv["app_version"])
	}
	if ctxv["llm_host"] != "gw.example.com" {
		t.Errorf("llm_host 应只有主机名，实得 %v", ctxv["llm_host"])
	}
	if ctxv["model"] != "glm-4-plus" {
		t.Errorf("model = %v, want glm-4-plus", ctxv["model"])
	}

	// 整份归档里既不该有密钥，也不该有带 token 的路径
	id, _ := got["id"].(string)
	b, err := os.ReadFile(filepath.Join(feedbackDir(dataDir), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{apiKey, leakyToken} {
		if strings.Contains(string(b), forbidden) {
			t.Errorf("归档里出现了不该外发的片段 %q：%s", forbidden, b)
		}
	}
}

// TestFeedbackSubmit_ContextFromTaskArchive 上下文带的是**那一次**运行的终态与失败步骤。
//
// 「反馈这条」指的是用户点开的卡片，不是"系统最近跑的那个"。
// 失败步骤倒着找：一步可能重试多次，最后一次失败才是用户看到的那个错。
func TestFeedbackSubmit_ContextFromTaskArchive(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	writeArchive(t, dataDir, "task-fb-1", types.GoalResult{
		TaskID: "task-fb-1",
		Goal:   "把报告发到群里",
		Status: types.GoalFailed,
		Steps: []types.StepResult{
			{StepID: "s1", Tool: "web.search", Status: types.StepSucceeded},
			{StepID: "s2", Tool: "file.write", Status: types.StepFailed, Error: "权限不足"},
			{StepID: "s3", Tool: "http.post", Status: types.StepFailed, Error: "连接被重置"},
		},
	})
	ts := newTokenTestServer(srv)
	defer ts.Close()

	_, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind": "bug", "text": "发送环节一直失败", "task_id": "task-fb-1",
	})
	ctxv, _ := got["context"].(map[string]any)
	if ctxv["task_id"] != "task-fb-1" {
		t.Errorf("task_id = %v, want task-fb-1", ctxv["task_id"])
	}
	if ctxv["task_status"] != string(types.GoalFailed) {
		t.Errorf("task_status = %v, want failed", ctxv["task_status"])
	}
	if ctxv["failed_tool"] != "http.post" {
		t.Errorf("failed_tool 应为最后一个失败步骤 %q，实得 %v", "http.post", ctxv["failed_tool"])
	}
	// 归档里的错误正文不外发：只带工具名，不带 Output/Error
	id, _ := got["id"].(string)
	b, err := os.ReadFile(filepath.Join(feedbackDir(dataDir), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "连接被重置") {
		t.Errorf("归档里混进了步骤错误正文（只该带工具名）：%s", b)
	}
}

// TestFeedbackSubmit_RejectsBadInput 类型不认识、描述为空都要挡住，且**不留半成品**。
//
// 空描述是最容易被写成"那就存一条空的吧"的：一条没有文字的反馈在列表里看起来
// 像提交了却没生效，用户于是再提交一次。
func TestFeedbackSubmit_RejectsBadInput(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"类型不认识", map[string]any{"kind": "complaint", "text": "有点慢"}},
		{"类型缺失", map[string]any{"text": "有点慢"}},
		{"描述为空", map[string]any{"kind": "bug", "text": "   "}},
		{"描述超长", map[string]any{"kind": "bug", "text": strings.Repeat("字", maxFeedbackTextRunes+1)}},
	}
	for _, c := range cases {
		code, out, _ := fbDo(t, ts, "POST", "/api/feedback", c.body)
		if code != 400 {
			t.Errorf("%s：状态 = %d（want 400），响应 %v", c.name, code, out)
		}
	}
	// 一条都没落盘：拒绝的代价是零，不是"先写一个空壳再改"
	entries, err := os.ReadDir(feedbackDir(dataDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("被拒的提交不该留下文件，实得 %d 个", len(entries))
	}
}

// TestFeedbackSubmit_StoresAndServesScreenshot 截图按**文件头**判类型、由后端起名，
// 并且要能看得回来——历史列表里列得出名字却取不回图片，等于只留了个占位。
func TestFeedbackSubmit_StoresAndServesScreenshot(t *testing.T) {
	srv, _ := newArchiveFixture(t)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	code, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind":        "bug",
		"text":        "截图里是报错弹窗",
		"attachments": []any{map[string]any{"data": dataURL}},
	})
	if code != 201 {
		t.Fatalf("状态 = %d, want 201", code)
	}
	atts, _ := got["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachments = %v", got["attachments"])
	}
	a, _ := atts[0].(map[string]any)
	name, _ := a["name"].(string)
	if !strings.HasSuffix(name, ".png") {
		t.Errorf("文件名应以 sniff 出的扩展名结尾，实得 %q", name)
	}
	if mime, _ := a["mime"].(string); mime != "image/png" {
		t.Errorf("mime = %v, want image/png", a["mime"])
	}

	resp, err := http.Get(ts.URL + "/api/feedback/attachment?name=" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("取回截图状态 = %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if !bytes.Equal(body, pngBytes) {
		t.Error("取回的字节与原图不一致")
	}
}

// TestFeedbackSubmit_FakeImageRollsBack 改名成 .png 的脚本不该因为前端声称 image/png
// 就被当图片存下；而且拒绝之后不能留下无人认领的截图。
//
// 第①例刻意把**一张真图放在伪装的脚本前面**：只贴脚本的话，SaveAttachment 在第一张
// 就失败、什么都没写，回滚那段代码删不删都一样——诱饵得让"先写成功的部分"真的存在。
func TestFeedbackSubmit_FakeImageRollsBack(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	script := base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\nrm -rf /\n"))
	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	code, out, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind": "bug",
		"text": "第一张能存、第二张是伪装成图片的脚本",
		"attachments": []any{
			map[string]any{"data": png},
			map[string]any{"data": script},
		},
	})
	if code != 400 {
		t.Fatalf("伪装图片应回 400，实得 %d：%v", code, out)
	}
	// ② 张数超上限：也拒绝（上限存在的意义是别让一条反馈拖一相册）
	many := make([]any, 0, maxFeedbackAttachments+1)
	for i := 0; i <= maxFeedbackAttachments; i++ {
		many = append(many, map[string]any{"data": png})
	}
	if code, out, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind": "bug", "text": "一次贴太多", "attachments": many,
	}); code != 400 {
		t.Fatalf("超张数应回 400，实得 %d：%v", code, out)
	}
	entries, err := os.ReadDir(feedbackDir(dataDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("被拒的提交留下了 %d 个文件：%v", len(entries), entries)
	}
}

// TestFeedbackAttachment_RejectsTraversal 截图名会拼进文件路径，所以它必须只认
// 自己写出去的那个形状。诱饵刻意放在 <dataDir>/settings.json：
// `../settings.json` 正好解析得到它——诱饵放错位置，这条断言就是空的。
func TestFeedbackAttachment_RejectsTraversal(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	decoy := filepath.Join(dataDir, "settings.json")
	if err := os.WriteFile(decoy, []byte(`{"api_key":"不该被读到"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTokenTestServer(srv)
	defer ts.Close()

	for _, name := range []string{"../settings.json", `..\..\settings.json`, "settings.json", "", "fb-99999999-999999-ffff-1.png", "fb-20260924-120000-abcd-1.exe"} {
		resp, err := http.Get(ts.URL + "/api/feedback/attachment?name=" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || strings.Contains(string(body), "api_key") {
			t.Errorf("name=%q 不该读到 feedback/ 之外的文件：状态 %d，响应 %s", name, resp.StatusCode, body)
		}
	}
	if _, err := os.Stat(decoy); err != nil {
		t.Errorf("诱饵文件被动了：%v", err)
	}
}

// TestFeedbackDelete_RemovesArchiveAndScreenshots 带截图的反馈必须**删得干净**：
// 截到的可能是别人的窗口、别家的页面，"还留在你机器上"这句话要有出口。
func TestFeedbackDelete_RemovesArchiveAndScreenshots(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	_, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind":        "bug",
		"text":        "删掉这条，别留截图",
		"attachments": []any{map[string]any{"data": base64.StdEncoding.EncodeToString(pngBytes)}},
	})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatal("没拿到 id")
	}
	atts, _ := got["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("截图没存上：%v", got["attachments"])
	}

	if code, out, _ := fbDo(t, ts, "DELETE", "/api/feedback/"+id, nil); code != 200 {
		t.Fatalf("删除状态 = %d：%v", code, out)
	}
	left, _ := filepath.Glob(filepath.Join(feedbackDir(dataDir), id+"*"))
	if len(left) != 0 {
		t.Errorf("删除后仍有残留：%v", left)
	}
	// 再删一次：不存在要说"没有这条"，不是"删好了"
	if code, _, _ := fbDo(t, ts, "DELETE", "/api/feedback/"+id, nil); code != 404 {
		t.Errorf("重复删除应回 404，实得 %d", code)
	}
	if code, _, _ := fbDo(t, ts, "DELETE", "/api/feedback/not-a-real-id", nil); code != 404 {
		t.Errorf("形状不合的 id 应回 404，实得 %d", code)
	}
}

// ---------- 远端投递（本地必选 + 远端可插拔）----------

const secretWorkspace = `C:\Users\宁\Projects\不该出去的项目`
const secretAPIKey = "sk-e2e-key-9d2f4b7a"

// newDeliveryFixture 装一个"配了远端"的界面：远端是本地假服务器，
// 凭证与主机绑定的 key 走真实 credentials 存储（脱敏要挖的就是它）。
func newDeliveryFixture(t *testing.T, handler http.HandlerFunc) (*Server, string) {
	t.Helper()
	srv, dataDir := newArchiveFixture(t)
	remote := httptest.NewServer(handler)
	t.Cleanup(remote.Close)
	creds, err := credentials.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.SetCloudConfig(credentials.CloudConfig{SupabaseURL: remote.URL, SupabaseAnonKey: "anon"}); err != nil {
		t.Fatal(err)
	}
	if err := creds.SetLLMAPIKey(secretAPIKey, "open.bigmodel.cn"); err != nil {
		t.Fatal(err)
	}
	srv.Agent.Creds = creds
	srv.Agent.Cfg.Workspace = secretWorkspace
	srv.Agent.Cfg.LLM.BaseURL = "https://open.bigmodel.cn/api/paas/v4"
	srv.Agent.Cfg.LLM.Model = "glm-4-plus"
	return srv, dataDir
}

// countHandler 一个只计数、一律回 201 的假远端。
func countHandler() (http.HandlerFunc, func() int) {
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			n++
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		}, func() int {
			mu.Lock()
			defer mu.Unlock()
			return n
		}
}

// TestFeedbackSubmit_DeliversRedactedCopy 投递的是**脱敏后的副本**，本地归档留原文。
//
// 这是整条判据最难写对、也最容易被写成"看着对"的一处：
// ① 用户粘在描述里的工作区绝对路径（带用户名）不能出去——他点的是"提交反馈"，
//
//	不是"把我的目录结构发给开发者"；
//
// ② 当前生效的密钥更不能出去，哪怕它压根不在反馈字段里（这是"顺手整份序列化配置"
//
//	这类实现最容易漏的那一处）；
//
// ③ 截图只到元数据：图片内容一旦跟着投递走，"先脱敏再出门"就形同虚设；
// ④ 本地那份**必须还是原文**——只动副本，才能保证他复查时看到的就是自己写的。
//
//	把 deliverFeedback 里 Redact 的返回值直接写回归档，④ 就红。
func TestFeedbackSubmit_DeliversRedactedCopy(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var row map[string]any
		_ = json.Unmarshal(b, &row)
		mu.Lock()
		bodies = append(bodies, row)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}
	srv, dataDir := newDeliveryFixture(t, handler)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	problemText := `写入失败：cannot open ` + secretWorkspace + `\a.txt（key=` + secretAPIKey + `）`
	_, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind":        "bug",
		"text":        problemText,
		"attachments": []any{map[string]any{"data": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)}},
	})
	if got["delivery"] != string(types.FeedbackSent) {
		t.Fatalf("配了远端且送成功，状态应为 sent，实得 %v（note=%v）", got["delivery"], got["delivery_note"])
	}
	note, _ := got["delivery_note"].(string)
	if !strings.Contains(note, "替换了 2 处") {
		t.Errorf("投递备注该说明动过几处机密片段（用户有权知道发出去的和写下的不一样），实得 %q", note)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("远端收到 %d 个请求", len(bodies))
	}
	raw, _ := json.Marshal(bodies[0])
	for _, forbidden := range []string{secretWorkspace, secretAPIKey, "iVBOR"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("发出去的那份里有不该出去的东西 %q：%s", forbidden, raw)
		}
	}
	sent, _ := bodies[0]["text"].(string)
	if !strings.Contains(sent, feedback.RedactedMarker) {
		t.Errorf("外发副本该留脱敏记号：%s", sent)
	}
	if bodies[0]["llm_host"] != "open.bigmodel.cn" || bodies[0]["app_version"] != buildinfo.Version {
		t.Errorf("现场列没带上或不对：%v / %v", bodies[0]["llm_host"], bodies[0]["app_version"])
	}

	// 本地那份是原文：少了这条断言，"投递前脱敏"很容易被写成"落盘前脱敏"。
	// 解析后再比，而不是在 JSON 文本里找——落盘的 JSON 里反斜杠是转义过的。
	b, err := os.ReadFile(filepath.Join(feedbackDir(dataDir), got["id"].(string)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk types.Feedback
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(onDisk.Text, secretWorkspace) || !strings.Contains(onDisk.Text, secretAPIKey) {
		t.Errorf("本地归档被一起改掉了（用户复查时该看到自己写的原文）：%s", onDisk.Text)
	}
	if onDisk.Delivery != types.FeedbackSent {
		t.Errorf("投递状态没写回归档，刷新后就看不见\"已送达\"了：%+v", onDisk)
	}
}

// TestFeedbackSubmit_DeliveryFailureStaysLocalAndVisible 投不出去只改状态，不改提交结果。
//
// 三件事一起守：① 提交仍回 201（反馈已经在他机器上了，报失败等于骗他重填）；
// ② 状态是 failed 而不是 local_only——"没配"和"没送到"必须能分开说，
//
//	否则用户以为一切正常，而那条反馈根本没出去；
//
// ③ 失败原因**留在盘上**，不是只在这次响应里闪一下：刷新之后还得看得见，
//
//	否则红点没了，用户以为自己已经送达。
func TestFeedbackSubmit_DeliveryFailureStaysLocalAndVisible(t *testing.T) {
	srv, dataDir := newDeliveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"gateway blew up"}`)
	})
	ts := newTokenTestServer(srv)
	defer ts.Close()

	code, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{"kind": "bug", "text": "远端是坏的"})
	if code != 201 {
		t.Fatalf("投递失败不该让提交失败，状态 = %d：%v", code, got)
	}
	if got["delivery"] != string(types.FeedbackFailed) {
		t.Errorf("状态应为 failed，实得 %v", got["delivery"])
	}
	note, _ := got["delivery_note"].(string)
	if !strings.Contains(note, "未送达") || !strings.Contains(note, "500") {
		t.Errorf("备注该说清是没送到、为什么，实得 %q", note)
	}
	// 盘上那份也是 failed：状态只活在响应里就等于刷新之后消失
	b, err := os.ReadFile(filepath.Join(feedbackDir(dataDir), got["id"].(string)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk types.Feedback
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Delivery != types.FeedbackFailed || onDisk.DeliveryNote == "" {
		t.Errorf("归档里的投递状态不对：%+v", onDisk)
	}
}

// TestFeedbackResend_DoesNotRepeatSentRows 未送达的要能自救，已送达的不能重复插行。
//
// 只有"失败可见"却没有重试入口，等于给用户一个只能看着的红点；
// 反过来，resend 一条已经 sent 的记录会在表里多出一条重复行，
// "这条反馈收没收到"从此说不清。
func TestFeedbackResend_DoesNotRepeatSentRows(t *testing.T) {
	counts, sent := countHandler()
	srv, _ := newDeliveryFixture(t, counts)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	_, got, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{"kind": "suggestion", "text": "希望支持批量启用"})
	id, _ := got["id"].(string)
	if sent() != 1 {
		t.Fatalf("提交时应投递一次，实得 %d 次", sent())
	}
	// 再点一次"重新投递"：已经送到的不再重复送
	_, resent, _ := fbDo(t, ts, "POST", "/api/feedback/"+id+"/resend", nil)
	if got2, _ := resent["delivery"].(string); got2 != string(types.FeedbackSent) {
		t.Errorf("重投一条已送达的反馈后状态应仍是 sent，实得 %v", got2)
	}
	if n := sent(); n != 1 {
		t.Errorf("已送达的不该再送一次，远端收到 %d 个请求", n)
	}
	// 没配远端时重投要当场说清，不能回一个"成功"然后什么都不做
	srv.Agent.Creds = nil
	if code, out, _ := fbDo(t, ts, "POST", "/api/feedback/"+id+"/resend", nil); code != 400 {
		t.Errorf("没配远端应回 400，实得 %d：%v", code, out)
	}
	if code, _, _ := fbDo(t, ts, "POST", "/api/feedback/no-such-id/resend", nil); code != 404 {
		t.Errorf("没有这条反馈应回 404，实得 %d", code)
	}
}

// TestFeedbackContextPreview_MatchesWhatGetsArchived 提交前看到的现场，必须就是提交后存下的现场。
//
// 这个端点唯一的价值是"说真话"：它和提交路径共用 feedbackContext，所以预览不是
// 一份前端抄写的答案。等值比较因此是这条判据的全部——
// 哪天有人把预览改成另算一套（或前端自己拼字段），这一条就红。
// 顺带钉住两件事：预览里不能出现工作区路径与密钥，remote 要能说出远端是谁。
func TestFeedbackContextPreview_MatchesWhatGetsArchived(t *testing.T) {
	counts, _ := countHandler()
	srv, dataDir := newDeliveryFixture(t, counts)
	writeArchive(t, dataDir, "task-fb-prev", types.GoalResult{
		TaskID: "task-fb-prev", Goal: "导出报表", Status: types.GoalFailed,
		Steps: []types.StepResult{{StepID: "s1", Tool: "shell.exec", Status: types.StepFailed, Error: "退出码 1"}},
	})
	ts := newTokenTestServer(srv)
	defer ts.Close()

	code, prev, raw := fbDo(t, ts, "GET", "/api/feedback/context?task_id=task-fb-prev", nil)
	if code != 200 {
		t.Fatalf("预览状态 = %d：%s", code, raw)
	}
	if strings.Contains(string(raw), secretWorkspace) || strings.Contains(string(raw), secretAPIKey) {
		t.Errorf("预览里出现了不该出去的东西：%s", raw)
	}
	if prev["remote"] != "supabase" {
		t.Errorf("预览应说出远端是 supabase，实得 %v", prev["remote"])
	}
	want, _ := prev["context"].(map[string]any)
	if want["failed_tool"] != "shell.exec" || want["task_status"] != string(types.GoalFailed) {
		t.Errorf("预览字段不对：%v", want)
	}

	_, sent, _ := fbDo(t, ts, "POST", "/api/feedback", map[string]any{
		"kind": "bug", "text": "导出总是失败", "task_id": "task-fb-prev",
	})
	got, _ := sent["context"].(map[string]any)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("预览与实存不一致：预览 %v，实存 %v", want, got)
	}
}
