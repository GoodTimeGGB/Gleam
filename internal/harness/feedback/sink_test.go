package feedback

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gleam/pkg/types"
)

func sampleFeedback() *types.Feedback {
	return &types.Feedback{
		ID:   "fb-20260924-120000-ab12",
		Kind: types.FeedbackBug,
		Text: "点安装没反应",
		Attachments: []types.FeedbackAttachment{
			{Name: "fb-20260924-120000-ab12-1.png", Mime: "image/png", Bytes: 2048},
		},
		Context: types.FeedbackContext{
			AppVersion: "0.1.0", GoVersion: "go1.22.0", OS: "windows/amd64",
			Model: "glm-4-plus", LLMHost: "open.bigmodel.cn",
			TaskID: "task-1", TaskStatus: types.GoalFailed, FailedTool: "http.post",
		},
		CreatedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		Delivery:  types.FeedbackLocalOnly,
	}
}

// TestRedact_KeepsLocalOriginalAndMarksOutbound 脱敏只动**外发的那份副本**。
//
// 本地归档必须保留原文：那是用户自己写的、也是他要复查的东西；
// 而发出去的那一份里，已知的机密片段一处不留。
func TestRedact_KeepsLocalOriginalAndMarksOutbound(t *testing.T) {
	f := sampleFeedback()
	f.Text = `报错：cannot open C:\Users\宁\Desktop\proj\a.txt，key=sk-abcdef123456`
	secret := `C:\Users\宁\Desktop\proj`

	out, n := Redact(f, []string{secret, "sk-abcdef123456", ""})
	if n != 2 {
		t.Errorf("替换次数 = %d, want 2（空 secret 不该算一次，也不该到处插记号）", n)
	}
	if !strings.Contains(f.Text, secret) || !strings.Contains(f.Text, "sk-abcdef123456") {
		t.Errorf("原文被改动了：%s", f.Text)
	}
	if strings.Contains(out.Text, secret) || strings.Contains(out.Text, "sk-abcdef123456") {
		t.Errorf("副本里还留着机密片段：%s", out.Text)
	}
	if !strings.Contains(out.Text, RedactedMarker) {
		t.Errorf("副本该留下显式记号（不然对方以为用户就写了这么多）：%s", out.Text)
	}
	if out.ID != f.ID || out.Kind != f.Kind || out.Context.Model != f.Context.Model {
		t.Error("脱敏不该顺手改掉无关字段")
	}
}

// TestRedact_ScrubsContextFields 上下文字段也过一道：白名单挡的是"我们主动放什么"，
// 挡不住以后有人往这些字段里塞带路径的值。闸门已经在整份副本上生效，
// 加字段的人不必先记住这条规矩。
func TestRedact_ScrubsContextFields(t *testing.T) {
	f := sampleFeedback()
	f.Context.TaskID = `task-C:\Users\宁\x`
	f.Context.FailedTool = `C:\Users\宁\tools\http.post`
	out, n := Redact(f, []string{`C:\Users\宁`})
	if n != 2 {
		t.Fatalf("替换次数 = %d, want 2", n)
	}
	for _, v := range []string{out.Context.TaskID, out.Context.FailedTool} {
		if strings.Contains(v, `C:\Users\宁`) {
			t.Errorf("上下文字段漏了脱敏：%s", v)
		}
	}
}

// TestFeedbackRow_FlattenedAndNoImageBytes 投递的行形状：可筛的列 + 截图**只有元数据**。
//
// 断言"字节内容不在行里"是这条测试的全部意义：截图截到什么不由我们事先决定，
// 一旦投递层开始带上图片，"先脱敏再出门"这条线就形同虚设了。
func TestFeedbackRow_FlattenedAndNoImageBytes(t *testing.T) {
	f := sampleFeedback()
	row := feedbackRow(f)
	for _, col := range []string{"id", "kind", "text", "app_version", "os", "model",
		"llm_host", "task_id", "task_status", "failed_tool", "attachments", "created_at"} {
		if _, ok := row[col]; !ok {
			t.Errorf("行里缺列 %q（表是按这份清单建的，缺列就是建好表也插不进）", col)
		}
	}
	if _, ok := row["context"]; ok {
		t.Error("不该整个 context 嵌套塞进一行：摊平的列才筛得动")
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "iVBOR") || strings.Contains(string(b), "base64") {
		t.Errorf("行里出现了图片内容：%s", b)
	}
	// 截图名字留在 attachments 元数据里——它是"本机有这张图"的凭据
	if !strings.Contains(string(b), "fb-20260924-120000-ab12-1.png") {
		t.Errorf("attachments 元数据没带上：%s", b)
	}
}

// TestSupabaseSink_SendPostsRow 真实走一次 HTTP：请求形状（表路径、apikey 头、
// 最小回读偏好）与行内容都要对得上 PostgREST 的要求。
func TestSupabaseSink_SendPostsRow(t *testing.T) {
	var gotReq *http.Request
	var gotBody map[string]any
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(201)
	}))
	defer ts.Close()

	sink := NewSupabase(SupabaseTarget{URL: ts.URL + "/", AnonKey: "anon-key-1"}, nil)
	f := sampleFeedback()
	if err := sink.Send(context.Background(), f); err != nil {
		t.Fatalf("Send 失败：%v", err)
	}
	if gotPath != "/rest/v1/"+FeedbackTable {
		t.Errorf("落点 = %q, want %q（PostgREST 的插入端点是 /rest/v1/<表>）", gotPath, "/rest/v1/"+FeedbackTable)
	}
	if gotReq.Method != http.MethodPost {
		t.Errorf("方法 = %s, want POST", gotReq.Method)
	}
	if gotReq.Header.Get("apikey") != "anon-key-1" {
		t.Errorf("缺 apikey 头：%v", gotReq.Header)
	}
	if gotReq.Header.Get("Authorization") != "Bearer anon-key-1" {
		t.Errorf("缺 Authorization 头：%v", gotReq.Header)
	}
	if gotReq.Header.Get("Prefer") != "return=minimal" {
		t.Errorf("Prefer = %q, want return=minimal（回读整行等于把正文再传一次）", gotReq.Header.Get("Prefer"))
	}
	if gotBody["task_id"] != f.Context.TaskID || gotBody["failed_tool"] != f.Context.FailedTool {
		t.Errorf("行内容与反馈不符：%v", gotBody)
	}
}

// TestSupabaseSink_MissingTableSaysCreateIt 404 / 42P01 要说"去建表"，不是含糊的"投递失败"：
// 前者用户看完就知道该干什么，后者只会让他反复点重试。
func TestSupabaseSink_MissingTableSaysCreateIt(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
	}{
		{"404", http.StatusNotFound, `{"message":"Could not find the table public/feedback_reports"}`},
		{"42P01", http.StatusBadRequest, `{"code":"42P01","message":"relation \"public.feedback_reports\" does not exist"}`},
	}
	for _, c := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.code)
			_, _ = io.WriteString(w, c.body)
		}))
		sink := NewSupabase(SupabaseTarget{URL: ts.URL, AnonKey: "k"}, nil)
		err := sink.Send(context.Background(), sampleFeedback())
		ts.Close()
		if err == nil {
			t.Fatalf("%s：应报错", c.name)
		}
		if !strings.Contains(err.Error(), "建表") {
			t.Errorf("%s：错误里该让人知道要建表，实得 %v", c.name, err)
		}
	}
}

// TestSupabaseSink_OtherFailureKeepsReason 别的失败（这里是 500）要把状态码与原因带回来，
// 但只带一句——云端回的一整页 HTML 不该进用户机器的归档。
func TestSupabaseSink_OtherFailureKeepsReason(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("很长的云端报错正文 ", 100))
	}))
	defer ts.Close()
	err := NewSupabase(SupabaseTarget{URL: ts.URL, AnonKey: "k"}, nil).Send(context.Background(), sampleFeedback())
	if err == nil {
		t.Fatal("500 应报错")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("错误里该有状态码：%v", err)
	}
	if len([]rune(err.Error())) > 300 {
		t.Errorf("错误该收着长度，实得 %d 字", len([]rune(err.Error())))
	}
}

// TestSupabaseTargetConfigured 只有地址、没有 key 也算没配：那种请求一定 401，
// 送出去只会得到一条看不出原因的"投递失败"。
func TestSupabaseTargetConfigured(t *testing.T) {
	for _, c := range []struct {
		name   string
		target SupabaseTarget
		want   bool
	}{
		{"完整", SupabaseTarget{URL: "https://x.supabase.co", AnonKey: "k"}, true},
		{"只有地址", SupabaseTarget{URL: "https://x.supabase.co"}, false},
		{"只有 key", SupabaseTarget{AnonKey: "k"}, false},
		{"都是空白", SupabaseTarget{URL: "  ", AnonKey: "  "}, false},
	} {
		if got := c.target.Configured(); got != c.want {
			t.Errorf("%s：Configured() = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSupabaseSinkRecordsEgress 反馈投递必须能自证"发去了哪个主机、多大"。
//
// 用户点的是"提交反馈"，交出去的是他写的那段话加一份上下文快照。台账上那一行
// 若指不出留痕，就等于宣称了一条本机证明不了的出网——比不列更坏。
func TestSupabaseSinkRecordsEgress(t *testing.T) {
	var gotHost string
	var gotBytes int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
	}))
	defer ts.Close()

	sink := NewSupabase(SupabaseTarget{URL: ts.URL, AnonKey: "k"}, func(h string, n int) {
		gotHost, gotBytes = h, n
	})
	if err := sink.Send(context.Background(), sampleFeedback()); err != nil {
		t.Fatalf("Send 失败：%v", err)
	}
	if want := strings.TrimPrefix(ts.URL, "http://"); gotHost != want {
		t.Errorf("出网留痕的主机 = %q，应为 %q", gotHost, want)
	}
	if gotBytes <= 0 {
		t.Errorf("出网留痕应带上请求体大小，实得 %d", gotBytes)
	}
}

// TestSupabaseSinkRecordsEgressOnFailure 连不上也要记：留痕发生在发包之前。
//
// 反过来（成功才记）会得到一个最坏的口径——"这台机器没往那个主机发过东西"，
// 而事实是发了、只是没成。在审计里"少记一笔"与"没发生过"长得一模一样。
func TestSupabaseSinkRecordsEgressOnFailure(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead := ts.URL
	ts.Close() // 服务已经关了，这一次发包一定失败

	sink := NewSupabase(SupabaseTarget{URL: dead, AnonKey: "k"}, func(string, int) { calls++ })
	if err := sink.Send(context.Background(), sampleFeedback()); err == nil {
		t.Fatal("连不上应报错")
	}
	if calls != 1 {
		t.Errorf("投递失败也要留痕一次，实际 %d 次", calls)
	}
}
