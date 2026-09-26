package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gleam/internal/config"
	"gleam/internal/harness/credentials"
	"gleam/internal/llm"
)

// glmHost 一条真实的官方入口，用来测"密钥绑到了哪台主机"。
const glmHost = "https://open.bigmodel.cn/api/paas/v4"

// scopeOf 复用生产的 host 解析，不在测试里另抄一份——抄一份就会和实现漂移。
func scopeOf(baseURL string) string { return llm.KeyScope(baseURL) }

// ---------- 设置与上下文 API ----------

func TestWebUI_SettingsGetMasked(t *testing.T) {
	f := newFixture(t, nil)
	s := f.call("GET", "/api/settings", nil)
	if s["persona"] == nil || s["agent"] == nil || s["llm"] == nil {
		t.Fatalf("settings 视图缺 section: %v", s)
	}
	llm := s["llm"].(map[string]any)
	if _, hasKey := llm["api_key"]; hasKey {
		t.Error("不应回传 api_key 明文")
	}
	if llm["api_key_set"] != false {
		t.Errorf("api_key_set = %v", llm["api_key_set"])
	}
}

// TestWebUI_EngineRangeRejection 引擎参数越界必须整次拒绝并报错（曾因 posInt 无上限，
// done_threshold=150 / max_replans=999 被"保存成功"静默接受）。max_steps=0 是合法兜底值。
func TestWebUI_EngineRangeRejection(t *testing.T) {
	f := newFixture(t, nil)
	post := func(body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		resp, err := http.Post(f.ts.URL+"/api/settings", "application/json", strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, out := post(map[string]any{"agent": map[string]any{"done_threshold": 150}}); code < 400 {
		t.Errorf("done_threshold=150 应被拒绝，得到 %d %v", code, out)
	}
	if f.agent.Cfg.Agent.DoneThreshold == 150 {
		t.Error("越界值被写入配置")
	}
	if code, out := post(map[string]any{"agent": map[string]any{"max_replans": 999}}); code < 400 {
		t.Errorf("max_replans=999 应被拒绝，得到 %d %v", code, out)
	}
	// 合法补丁里混一个越界字段：整次拒绝，合法字段也不许夹带生效
	if code, _ := post(map[string]any{"agent": map[string]any{"max_replans": 5, "done_threshold": 200}}); code < 400 {
		t.Errorf("混合越界应整次拒绝，得到 %d", code)
	}
	if f.agent.Cfg.Agent.MaxReplans == 5 {
		t.Error("被拒绝的请求里合法字段不应生效")
	}
	// 边界内值（含 max_steps=0 的"用默认"语义）正常接受
	if code, out := post(map[string]any{"agent": map[string]any{"max_steps": 0, "done_threshold": 90}}); code >= 400 {
		t.Errorf("合法值被拒，得到 %d %v", code, out)
	}
	if f.agent.Cfg.Agent.DoneThreshold != 90 {
		t.Errorf("done_threshold = %d，期望 90", f.agent.Cfg.Agent.DoneThreshold)
	}
}

func TestWebUI_SettingsApplyAndPersist(t *testing.T) {
	f := newFixture(t, nil)
	f.agent.Cfg.LLM.Provider = "glm" // 非 mock 时 ApplySettings 才会热更新 GLM 客户端字段
	updated := f.call("POST", "/api/settings", map[string]any{
		"persona": map[string]any{"style": "gentle", "name": "小光"},
		"agent":   map[string]any{"step_retries": 3, "context_compress": false},
		"safety":  map[string]any{"mode": "plan_first", "approval_timeout_seconds": 60},
	})
	persona := updated["persona"].(map[string]any)
	if persona["style"] != "gentle" || persona["name"] != "小光" {
		t.Errorf("persona = %v", persona)
	}
	agentSec := updated["agent"].(map[string]any)
	if agentSec["step_retries"] != float64(3) || agentSec["context_compress"] != false {
		t.Errorf("agent = %v", agentSec)
	}
	if updated["safety"].(map[string]any)["mode"] != "plan_first" {
		t.Error("safety.mode 未更新")
	}
	// 运行时热生效
	if f.agent.Cfg.Safety.Mode != "plan_first" {
		t.Error("cfg.Safety.Mode 未生效")
	}
	// 覆盖层已持久化，可被新配置加载恢复
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, filepath.Join(f.dataDir, config.OverlayFile)); err != nil {
		t.Fatal(err)
	}
	if fresh.Persona.Style != "gentle" || fresh.Agent.StepRetries != 3 {
		t.Errorf("覆盖层未持久化: %+v %+v", fresh.Persona, fresh.Agent)
	}
}

// TestWebUI_CostGovernanceSettingsRoundTrip 成本治理四件套的设置项应能保存、热生效并持久化。
func TestWebUI_CostGovernanceSettingsRoundTrip(t *testing.T) {
	f := newFixture(t, nil)
	updated := f.call("POST", "/api/settings", map[string]any{
		"agent": map[string]any{
			"dedupe_calls":              false,
			"max_llm_calls_per_task":    7,
			"max_tokens_per_task":       12345,
			"max_task_duration_seconds": 42,
		},
		"llm": map[string]any{"fast_model": "glm-cheap"},
	})
	agentSec := updated["agent"].(map[string]any)
	if agentSec["dedupe_calls"] != false {
		t.Errorf("dedupe_calls = %v", agentSec["dedupe_calls"])
	}
	for k, want := range map[string]float64{
		"max_llm_calls_per_task": 7, "max_tokens_per_task": 12345, "max_task_duration_seconds": 42,
	} {
		if got := agentSec[k].(float64); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if got := updated["llm"].(map[string]any)["fast_model"]; got != "glm-cheap" {
		t.Errorf("fast_model = %v", got)
	}
	// 运行时热生效
	if f.agent.Cfg.Agent.DedupeCalls || f.agent.Cfg.Agent.MaxLLMCallsPerTask != 7 ||
		f.agent.Cfg.Agent.MaxTokensPerTask != 12345 || f.agent.Cfg.Agent.MaxTaskDurationSecs != 42 ||
		f.agent.Cfg.LLM.FastModel != "glm-cheap" {
		t.Errorf("运行时配置未生效: agent=%+v fast=%q", f.agent.Cfg.Agent, f.agent.Cfg.LLM.FastModel)
	}
	// 覆盖层持久化
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, filepath.Join(f.dataDir, config.OverlayFile)); err != nil {
		t.Fatal(err)
	}
	if fresh.Agent.MaxLLMCallsPerTask != 7 || fresh.Agent.MaxTokensPerTask != 12345 ||
		fresh.Agent.MaxTaskDurationSecs != 42 || fresh.LLM.FastModel != "glm-cheap" {
		t.Errorf("覆盖层未持久化: agent=%+v fast=%q", fresh.Agent, fresh.LLM.FastModel)
	}
}

// TestWebUI_BudgetZeroMeansUnlimited 预算填 0 应被接受（0 = 不限），不能因为"非正数"被拒。
func TestWebUI_BudgetZeroMeansUnlimited(t *testing.T) {
	f := newFixture(t, nil)
	updated := f.call("POST", "/api/settings", map[string]any{
		"agent": map[string]any{
			"max_llm_calls_per_task":    0,
			"max_tokens_per_task":       0,
			"max_task_duration_seconds": 0,
		},
	})
	agentSec := updated["agent"].(map[string]any)
	for _, k := range []string{"max_llm_calls_per_task", "max_tokens_per_task", "max_task_duration_seconds"} {
		if got := agentSec[k].(float64); got != 0 {
			t.Errorf("%s = %v, want 0", k, got)
		}
	}
	if f.agent.Cfg.Agent.MaxLLMCallsPerTask != 0 || f.agent.Cfg.Agent.MaxTokensPerTask != 0 ||
		f.agent.Cfg.Agent.MaxTaskDurationSecs != 0 {
		t.Errorf("0 预算未生效: %+v", f.agent.Cfg.Agent)
	}
}

func TestWebUI_SettingsInvalidPatch(t *testing.T) {
	f := newFixture(t, nil)
	// 全部字段非法 → 400
	req := map[string]any{
		"persona": map[string]any{"style": "狂野"},
		"safety":  map[string]any{"mode": "yolo"},
		"agent":   map[string]any{"step_retries": "abc"},
	}
	resp, err := http.Post(f.ts.URL+"/api/settings", "application/json", strings.NewReader(`{"persona":{"style":"狂野"},"safety":{"mode":"yolo"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("非法补丁应 400, got %d", resp.StatusCode)
	}
	_ = req
	// 非法值不应污染现有配置
	if f.agent.Cfg.Persona.Style == "狂野" || f.agent.Cfg.Safety.Mode == "yolo" {
		t.Error("非法值被应用")
	}
}

func TestWebUI_ContextCompressAndClear(t *testing.T) {
	f := newFixture(t, nil)
	// 制造溢出：容量 20，写入 22 轮（每轮足够长，压缩收益为正）
	long := strings.Repeat("需要保留的关键上下文细节。", 12)
	for i := 0; i < 22; i++ {
		f.agent.Mem.AddTurn("user", long)
	}
	ctx := f.call("GET", "/api/context", nil)
	if ctx["enabled"] != true || ctx["overflow"].(float64) < 2 {
		t.Fatalf("context = %v", ctx)
	}
	out := f.call("POST", "/api/context/compress", nil)
	if out["compressed"] != true {
		t.Errorf("compress = %v", out)
	}
	ctxAfter := out["context"].(map[string]any)
	if ctxAfter["overflow"].(float64) != 0 || ctxAfter["summary"].(string) == "" {
		t.Errorf("压缩后 = %v", ctxAfter)
	}
	// token 收益估算应为正（溢出原文远大于摘要）
	if ctxAfter["est_tokens_saved"].(float64) < 0 {
		t.Errorf("est_tokens_saved = %v", ctxAfter["est_tokens_saved"])
	}
	cleared := f.call("POST", "/api/context/clear", nil)
	if cleared["cleared"] != true {
		t.Errorf("clear = %v", cleared)
	}
	if cleared["context"].(map[string]any)["summary"].(string) != "" {
		t.Error("清空后摘要应为空")
	}
}

// TestWebUI_APIKeySaveAndClear 密钥保存全链路：写入凭证文件、视图只回布尔、
// 覆盖层零泄漏、空串=不修改、clear_api_key 显式清除（内存 + 磁盘同时生效）。
// 密钥按主机绑定，所以这里必须带上 base_url——没有主机的密钥无处可绑。
func TestWebUI_APIKeySaveAndClear(t *testing.T) {
	f := newFixture(t, nil)
	cred, err := credentials.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.agent.Creds = cred

	up := f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": glmHost, "api_key": "sk-web-abcdef"},
	})
	llmSec := up["llm"].(map[string]any)
	if llmSec["api_key_set"] != true {
		t.Errorf("保存后 api_key_set = %v", llmSec["api_key_set"])
	}
	if got, _ := cred.ResolveLLMAPIKey(scopeOf(glmHost)); got != "sk-web-abcdef" {
		t.Errorf("凭证文件未落盘: %q", got)
	}
	if _, hasPlain := llmSec["api_key"]; hasPlain {
		t.Error("响应不应回传 api_key")
	}
	// 覆盖层（settings.yaml）绝不能带上 key
	raw, err := os.ReadFile(filepath.Join(f.dataDir, config.OverlayFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-web-abcdef") {
		t.Error("settings.yaml 泄漏了 api_key")
	}

	// 前端"留空=不修改"：空串不得动已存的 key
	f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": glmHost, "api_key": "", "model": "m-next"},
	})
	if got, _ := cred.ResolveLLMAPIKey(scopeOf(glmHost)); got != "sk-web-abcdef" {
		t.Errorf("空串不应清除，得到 %q", got)
	}

	// 显式清除：内存与磁盘同时生效
	up2 := f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": glmHost, "clear_api_key": true},
	})
	if up2["llm"].(map[string]any)["api_key_set"] != false {
		t.Errorf("清除后 api_key_set = %v", up2["llm"].(map[string]any)["api_key_set"])
	}
	if got, host := cred.ResolveLLMAPIKey(scopeOf(glmHost)); got != "" || host != "" {
		t.Errorf("磁盘未清除: %q / %q", got, host)
	}
	if f.agent.Cfg.LLM.APIKey != "" {
		t.Errorf("内存未清除: %q", f.agent.Cfg.LLM.APIKey)
	}
}

// authCapture 记录假厂商收到的鉴权头。按厂商各记一份，而不是按路径记：
// 客户端会往 base_url 后面拼 /chat/completions 等路径，拼法属于 llm 层，
// 测试若把这些写进键名，llm 一改路径这里就成假绿。
type authCapture struct {
	mu    sync.Mutex
	hits  int
	auths []string
}

func (c *authCapture) record(auth string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hits++
	c.auths = append(c.auths, auth)
}

// summary 返回请求次数与**第一条非空鉴权头**：按"有没有一条非空"判，
// 而不是把记录拼起来比字符串——拼法会让"两次都空"看起来像"收到过东西"。
func (c *authCapture) summary() (hits int, nonEmptyAuth string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.auths {
		if a != "" {
			return c.hits, a
		}
	}
	return c.hits, ""
}

// recordingKeyServer 起一个假厂商：答应答、记下鉴权头。
func recordingKeyServer(t *testing.T, cap *authCapture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			auth = r.Header.Get("x-api-key")
		}
		cap.record(auth)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"data":[{"id":"m-b"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWebUI_APIKeyNotForwardedToOtherHost 换厂商不得把上一家的密钥发出去。
//
// 修之前：密钥只按"有没有"管理，设置页把 base_url 从 A 改成 B 之后，
// 新端点照样收到 A 的 Bearer 头——本地优先的产品替用户把凭证转发给了第三方，
// 而界面上写着"已设置"，没有任何一处会提示。判据在 credentials 那层已经对，
// 这里要证的是**线路上真的没发**（本仓库栽过的是"判据对、线没接"）。
func TestWebUI_APIKeyNotForwardedToOtherHost(t *testing.T) {
	var capA, capB authCapture
	srvA := recordingKeyServer(t, &capA)
	srvB := recordingKeyServer(t, &capB)
	urlA, urlB := srvA.URL, srvB.URL

	f := newFixture(t, nil)
	cred, err := credentials.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.agent.Creds = cred
	f.agent.Cfg.LLM.Provider = "glm" // mock 下不建真实客户端，这条要测真实外发

	// ① 给 A 家配密钥
	v := f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": urlA, "model": "m-a", "api_key": "sk-A-只属于A"},
	})
	if v["llm"].(map[string]any)["api_key_set"] != true {
		t.Fatalf("A 家应显示已设置: %v", v["llm"])
	}

	// ② 只换厂商、不填密钥
	v2 := f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": urlB, "model": "m-b"},
	})
	l2 := v2["llm"].(map[string]any)
	if l2["api_key_set"] != false {
		t.Errorf("换到 B 家后不该显示已设置: %v", l2["api_key_set"])
	}
	if l2["api_key_host"] != scopeOf(urlA) {
		t.Errorf("应回传密钥归属主机以便界面说明，得到 %v", l2["api_key_host"])
	}
	if f.agent.Cfg.LLM.APIKey != "" {
		t.Errorf("内存里的生效密钥应已让位，得到 %q", f.agent.Cfg.LLM.APIKey)
	}

	// ③ 真往 B 发一次（连接自测走的就是 llmFormTarget 这条取密钥的路）
	res := f.call("POST", "/api/llm/test", map[string]any{"base_url": urlB, "model": "m-b"})
	if res["ok"] != true {
		t.Fatalf("探测应打到本地假服务，得到 %v", res)
	}
	if hits, auths := capB.summary(); hits == 0 {
		t.Fatal("B 家一次都没收到请求，这条断言就是空的")
	} else if strings.Contains(auths, "sk-A") {
		t.Errorf("B 家收到了 A 家的密钥：%q", auths)
	} else if auths != "" {
		t.Errorf("B 家不该收到任何鉴权头：%q", auths)
	}

	// ④ 换回 A：原来那把还在，不用重填
	v3 := f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": urlA, "model": "m-a"},
	})
	if v3["llm"].(map[string]any)["api_key_set"] != true {
		t.Fatalf("换回 A 家应恢复已设置: %v", v3["llm"])
	}
	f.call("POST", "/api/llm/test", map[string]any{"base_url": urlA, "model": "m-a"})
	if hits, auths := capA.summary(); hits == 0 || !strings.Contains(auths, "sk-A-只属于A") {
		t.Errorf("A 家应收到原密钥（切回来免重填），得到 %d 次 / %q", hits, auths)
	}
}

// TestWebUI_APIKeyNeedsHost 没有主机就没法绑：填了 key 却没填 base_url 必须报错，
// 而不是悄悄存成"没绑主机的密钥"——那种记录会被下一次的补绑逻辑随手发给第一台主机。
func TestWebUI_APIKeyNeedsHost(t *testing.T) {
	f := newFixture(t, nil)
	cred, err := credentials.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.agent.Creds = cred
	f.agent.Cfg.LLM.BaseURL = ""

	code, msg := f.status("POST", "/api/settings", `{"llm":{"api_key":"sk-无处可绑"}}`)
	if code != 400 {
		t.Fatalf("状态码 = %d (%s)，应为 400", code, msg)
	}
	if !strings.Contains(msg, "base_url") {
		t.Errorf("应说明缺 base_url，得到 %q", msg)
	}
	if got, _ := cred.ResolveLLMAPIKey(""); got != "" {
		t.Errorf("不该落盘，得到 %q", got)
	}
}

// TestWebUI_BaseURLClear 清空 base_url 必须真的回落（曾因 getStr 跳过空串而静默失效）。
func TestWebUI_BaseURLClear(t *testing.T) {
	f := newFixture(t, nil)
	f.agent.Cfg.LLM.Provider = "glm"
	f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": "https://custom.example/v4"},
	})
	if f.agent.Cfg.LLM.BaseURL == "" {
		t.Fatal("base_url 未写入")
	}
	f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"base_url": ""},
	})
	// 清空后内存为空串，启动/保存路径会经 ResolvePreset 回落厂商预设
	if f.agent.Cfg.LLM.BaseURL != "" {
		t.Errorf("清空未生效: %q", f.agent.Cfg.LLM.BaseURL)
	}
}

// TestWebUI_LLMTestProbe 连接探测：成功/鉴权失败分类/mock 短路。
func TestWebUI_LLMTestProbe(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") == "" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	})
	gwOK := httptest.NewServer(ok)
	defer gwOK.Close()
	gw401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer gw401.Close()

	f := newFixture(t, nil)
	f.agent.Cfg.LLM.Provider = "glm" // mock 会短路，这里要真实走 HTTP 栈
	res := f.call("POST", "/api/llm/test", map[string]any{
		"base_url": gwOK.URL, "model": "probe-model", "api_key": "k", "protocol": "openai_chat",
	})
	if res["ok"] != true || res["model"] != "probe-model" {
		t.Errorf("探测成功路径 = %v", res)
	}
	res2 := f.call("POST", "/api/llm/test", map[string]any{
		"base_url": gw401.URL, "model": "m", "api_key": "bad", "protocol": "openai_chat",
	})
	if res2["ok"] != false || res2["kind"] != "auth" || res2["http_status"] != float64(401) {
		t.Errorf("401 应分类为 auth，得到 %v", res2)
	}
	// mock 供应商：不联网，直接给出 mock 结论
	f.agent.Cfg.LLM.Provider = "mock"
	res3 := f.call("POST", "/api/llm/test", map[string]any{})
	if res3["kind"] != "mock" || res3["ok"] != true {
		t.Errorf("mock 短路 = %v", res3)
	}
	// M4：mock 会话下表单填了显式 base_url → 必须真实探测（切换供应商前预验证是唯一用途）
	res4 := f.call("POST", "/api/llm/test", map[string]any{
		"base_url": gwOK.URL, "model": "m", "api_key": "k", "protocol": "openai_chat",
	})
	if res4["kind"] != "ok" || res4["ok"] != true {
		t.Errorf("显式 base_url 应跳过 mock 短路，得到 %v", res4)
	}
}

// TestWebUI_LLMModelsFetch 在线拉模型列表：成功解析 / 失败分类 / mock 短路。
func TestWebUI_LLMModelsFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m-a","owned_by":"x"},{"id":"m-b"}]}`))
	}))
	defer srv.Close()

	f := newFixture(t, nil)
	f.agent.Cfg.LLM.Provider = "glm"
	res := f.call("POST", "/api/llm/models", map[string]any{
		"base_url": srv.URL + "/v1", "api_key": "k", "protocol": "openai_chat",
	})
	if res["ok"] != true || res["count"] != float64(2) {
		t.Fatalf("拉取结果 = %v", res)
	}
	list, _ := res["models"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["id"] != "m-a" {
		t.Fatalf("models = %v", res["models"])
	}
	res2 := f.call("POST", "/api/llm/models", map[string]any{
		"base_url": srv.URL + "/nope", "api_key": "bad", "protocol": "openai_chat",
	})
	if res2["ok"] != false || res2["kind"] != "auth" {
		t.Errorf("401 应分类为 auth，得到 %v", res2)
	}
	f.agent.Cfg.LLM.Provider = "mock"
	res3 := f.call("POST", "/api/llm/models", map[string]any{})
	if res3["kind"] != "mock" || res3["ok"] != true {
		t.Errorf("mock 短路 = %v", res3)
	}
	// M4：显式 base_url 的表单值必须穿透 mock 短路真实拉取
	res4 := f.call("POST", "/api/llm/models", map[string]any{
		"base_url": srv.URL + "/v1", "api_key": "k", "protocol": "openai_chat",
	})
	if res4["kind"] != "ok" || res4["count"] != float64(2) {
		t.Errorf("显式 base_url 应跳过 mock 短路，得到 %v", res4)
	}
}

func TestWebUI_SettingsPageRendered(t *testing.T) {
	f := newFixture(t, nil)
	resp, err := http.Get(f.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	_, _ = io.Copy(&b, resp.Body)
	body := b.String()
	if !strings.Contains(body, "view-settings") || !strings.Contains(body, "记忆与上下文") {
		t.Error("设置视图未内嵌到首页")
	}
}
