package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gleam/internal/harness/credentials"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

func rowByID(led ConnectionLedger, id string) (ConnectionRow, bool) {
	for _, r := range led.Rows {
		if r.ID == id {
			return r, true
		}
	}
	return ConnectionRow{}, false
}

// TestConnections_EveryEgressKindHasARow 出网清单与台账行**双向**对上。
//
// 这是这道判据唯一有意义的形状：单向只挡一半。
//   - 清单里有、台账没画 → 那条出网路径在界面上不存在，"连了而没交代"就是这么发生的；
//   - 台账画了、清单里没有 → 这一行没有对应的留痕落点，读数永远空白，
//     而读者分不清"没有记录"和"这行不归我管"。
//
// 闸门 scripts/check-egress-owner.py 只能读源码，判不出"这一行今天到底画没画出来"
// （函数可能压根没被调用），所以这条必须在运行时判。
func TestConnections_EveryEgressKindHasARow(t *testing.T) {
	f := newFixture(t, nil)
	led := f.a.ConnectionView("127.0.0.1:8787")

	listed := map[string]bool{}
	for _, k := range EgressKinds() {
		listed[k] = true
	}
	drawn := map[string]bool{}
	for _, r := range led.Rows {
		if r.Kind != "out" {
			continue
		}
		drawn[r.ID] = true
	}
	for _, kind := range EgressKinds() {
		r, ok := rowByID(led, kind)
		if !ok {
			t.Errorf("出网清单里的 %q 在台账上没有对应的行：这条线在界面上等于不存在", kind)
			continue
		}
		if r.Kind != "out" {
			t.Errorf("%q 这一行的 kind = %q，出网落点必须是 out", kind, r.Kind)
		}
	}
	for id := range drawn {
		if !listed[id] {
			t.Errorf("台账画了出网行 %q，但它不在出网清单里：这一行不会有留痕读数，空白格子的读法是\"不归我管\"", id)
		}
	}
}

// TestConnections_NoRowHasAnEmptyCell 每格都得有话，空白在这张表里是一种说谎。
//
// 台账的承诺是"每一行都交代了通到哪/谁能触发/留痕在哪/想关动哪里"。少一格不是"信息不足"，
// 而是读者会拿自己脑补的那版补上——本地优先的产品最不该把补白交给用户想象。
// 另外：**关键动作没留痕的行必须在 Trace 里把自己说成没留痕**，否则 Untraced 只是个布尔值，
// 界面渲染错了也没人知道。
func TestConnections_NoRowHasAnEmptyCell(t *testing.T) {
	f := newFixture(t, nil)
	for _, bind := range []string{"", "127.0.0.1:8787", "0.0.0.0:8787"} {
		for _, r := range f.a.ConnectionView(bind).Rows {
			for _, field := range []struct{ name, val string }{
				{"title", r.Title}, {"kindText", r.KindText}, {"target", r.Target},
				{"status", r.Status}, {"trigger", r.Trigger}, {"leaves", r.Leaves},
				{"trace", r.Trace}, {"off", r.Off},
			} {
				if strings.TrimSpace(field.val) == "" {
					t.Errorf("bind=%q 行 %q 的 %s 是空的", bind, r.ID, field.name)
				}
			}
			if r.Kind == "out" && strings.TrimSpace(r.Stats) == "" {
				t.Errorf("bind=%q 出网行 %q 没有读数文案（没记录也要说成没记录）", bind, r.ID)
			}
			if r.Untraced && !strings.Contains(r.Trace, "没有留痕") {
				t.Errorf("行 %q 标了未留痕，Trace 却没说自己没留痕：%q", r.ID, r.Trace)
			}
		}
	}
}

// TestConnections_InboundRowFollowsRealBindAddr 本机服务这一行只画真实监听地址。
//
// 默认 fixture 不传地址 → 这一行**不出现**（不是"猜一个回环给自己壮胆"）；
// 传非回环地址 → 状态与警示都必须改口：口令挡得住网页，挡不住明文链路上的旁听，风险由绑定地址决定。
func TestConnections_InboundRowFollowsRealBindAddr(t *testing.T) {
	f := newFixture(t, nil)

	if _, ok := rowByID(f.a.ConnectionView(""), "inbound"); ok {
		t.Error("拿不到监听地址时不该画本机服务这一行")
	}

	loop, ok := rowByID(f.a.ConnectionView("127.0.0.1:8787"), "inbound")
	if !ok {
		t.Fatal("绑了回环就该有这一行")
	}
	if loop.Kind != "in" || loop.Target != "127.0.0.1:8787" {
		t.Errorf("回环行 = %+v", loop)
	}
	if !strings.Contains(loop.Alert, "回环") {
		t.Errorf("回环应说明\"只有本机能连、口令文件同用户可读\"：%q", loop.Alert)
	}

	open, ok := rowByID(f.a.ConnectionView("0.0.0.0:8787"), "inbound")
	if !ok {
		t.Fatal("绑了 0.0.0.0 也该有这一行")
	}
	if !strings.Contains(open.Status, "非回环") || !strings.Contains(open.Alert, "局域网") {
		t.Errorf("非回环必须改口报警：status=%q alert=%q", open.Status, open.Alert)
	}
}

// TestConnections_LlmRowHostComesFromKeyScope 模型行的主机必须等于密钥绑定的那把尺。
//
// 两处各算一遍主机名就会漂：密钥说"只发往 A"，台账写"B"，用户读到的是两份互相矛盾的
// 安全承诺。这里断的是**相等**，不是"非空"。
// 顺带断另一半：留痕里出现过别的主机时，这一行必须把话挑明（换厂商后旧接入仍在发过包）。
func TestConnections_LlmRowTargetMatchesKeyScope(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.LLM.Provider = "glm"
	f.a.Cfg.LLM.BaseURL = "https://api.example.com/v1"

	r, ok := rowByID(f.a.ConnectionView(""), "llm")
	if !ok {
		t.Fatal("缺模型行")
	}
	want := llm.KeyScope(f.a.Cfg.LLM.BaseURL)
	if want == "" {
		t.Fatal("前提不成立：KeyScope 解析不出主机")
	}
	if r.Target != want {
		t.Errorf("模型行目标 = %q，应等于密钥绑定的主机 %q", r.Target, want)
	}
	if r.Status != "开着" {
		t.Errorf("配了真实接入应说\"开着\"，得到 %q", r.Status)
	}
	if !strings.Contains(r.Leaves, "提示词") {
		t.Errorf("这一行的重点是把提示词全文发出去：%q", r.Leaves)
	}

	// 留痕里有第三个主机：说明发包时的接入地址与设置页现在写的不是同一个。
	f.a.Gate.RecordEgress("llm", "old.vendor.com", 2048)
	r2, _ := rowByID(f.a.ConnectionView(""), "llm")
	if !strings.Contains(r2.Alert, "old.vendor.com") {
		t.Errorf("留痕与当前接入不一致时要点名：%q", r2.Alert)
	}
	if !strings.Contains(r2.Stats, "1 次") || !strings.Contains(r2.Stats, "2.0 KB") {
		t.Errorf("读数没接上门控留痕：%q", r2.Stats)
	}
}

// TestConnections_MockModelSaysTheLineCannotSend 模拟模型下不许写得像真在发包。
func TestConnections_MockModelSaysTheLineCannotSend(t *testing.T) {
	f := newFixture(t, nil) // fixture 默认 provider = mock
	r, ok := rowByID(f.a.ConnectionView(""), "llm")
	if !ok {
		t.Fatal("缺模型行")
	}
	if !strings.Contains(r.Status, "发不出东西") || !strings.Contains(r.Leaves, "不外发") {
		t.Errorf("mock 下的说法 = status %q / leaves %q", r.Status, r.Leaves)
	}
}

// TestConnections_WebFetchStatusComesFromGate 抓取这一行的重点是"要不要你点头"，
// 而这件事归门控判，不归台账文案自己感觉——所以两条分支都要能从权限里读出来。
func TestConnections_WebFetchStatusComesFromGate(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Reg.MustRegister(&funcTool{name: "web.fetch", perm: types.PermissionReadOnly,
		fn: func(context.Context, map[string]any) (any, error) { return "ok", nil }})

	r, _ := rowByID(f.a.ConnectionView(""), "web.fetch")
	if !strings.Contains(r.Status, "自动放行") {
		t.Errorf("只读权限应说自动放行：%q", r.Status)
	}
	if !strings.Contains(r.Target, "模型") {
		t.Errorf("这一行的关键是网址由模型自己挑，不是用户配的：%q", r.Target)
	}

	// 收紧成"每次问你"后，界面必须跟着改口——判据来自门控，不是写死的文案。
	f.a.Gate.SetToolPermission("web.fetch", types.PermissionUserApproved)
	r2, _ := rowByID(f.a.ConnectionView(""), "web.fetch")
	if !strings.Contains(r2.Status, "每次问你") {
		t.Errorf("权限改紧后应改口：%q", r2.Status)
	}

	f.a.Cfg.Safety.AllowPrivateWeb = true
	r3, _ := rowByID(f.a.ConnectionView(""), "web.fetch")
	if !strings.Contains(r3.Alert, "内网") {
		t.Errorf("放开内网抓取必须警示：%q", r3.Alert)
	}
}

// TestConnections_CloudAndFeedbackAbsentWhenUnconfigured 两条可选出网路径：
// 没配就要说"这条线不存在"，不能留一行看起来"随时会发"的空壳。
// 判定与凭证层同一把尺（地址+key 两件齐才算配好），否则"已配置"就是说谎。
func TestConnections_CloudAndFeedbackAbsentWhenUnconfigured(t *testing.T) {
	f := newFixture(t, nil)

	// ① 凭证存储都没装配
	for _, id := range []string{"cloud", "feedback"} {
		r, ok := rowByID(f.a.ConnectionView(""), id)
		if !ok {
			t.Fatalf("缺 %s 行", id)
		}
		if !strings.Contains(r.Status, "没装配") {
			t.Errorf("%s 行状态 = %q", id, r.Status)
		}
	}

	cred, err := credentials.Open(f.a.Cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Creds = cred

	// ② 有存储但没填项目地址
	cloud, _ := rowByID(f.a.ConnectionView(""), "cloud")
	if !strings.Contains(cloud.Status, "未配置") {
		t.Errorf("没配项目地址应说未配置：%q", cloud.Status)
	}
	fb, _ := rowByID(f.a.ConnectionView(""), "feedback")
	if !strings.Contains(fb.Status, "只落本机") {
		t.Errorf("反馈没配远端应说只落本机：%q", fb.Status)
	}

	// ③ 只填地址不填 key：请求一定 401，这一行不许说"已配置"
	if err := cred.SetCloudConfig(credentials.CloudConfig{SupabaseURL: "https://proj.supabase.co"}); err != nil {
		t.Fatal(err)
	}
	half, _ := rowByID(f.a.ConnectionView(""), "feedback")
	if !strings.Contains(half.Status, "未配置") {
		t.Errorf("缺 anon key 不该算配好：%q", half.Status)
	}

	// ④ 两件都齐：目标取 KeyScope，与密钥绑定同一把尺
	if err := cred.SetCloudConfig(credentials.CloudConfig{SupabaseAnonKey: "anon"}); err != nil {
		t.Fatal(err)
	}
	full, _ := rowByID(f.a.ConnectionView(""), "cloud")
	if full.Status != "已配置" || full.Target != llm.KeyScope("https://proj.supabase.co") {
		t.Errorf("配好后 = %+v", full)
	}
}

// TestConnections_EgressScopeTextFollowsScanWindow 读数范围必须跟着读数说清"数了哪些"。
//
// 内存环只有最近若干条、重启即清零。只印"N 次 / M 字节"就是把"我不知道"讲成答案。
// 三种范围三种说法：一条都没有 / 数了本次这些 / **环写满了**（这时"最近 200 条"才是实话，
// 说成"本次运行的全部"就多报了）。
// 另一半判据：没开审计落盘时不许指路去查落盘文件——那句指路会把"没记"伪装成"没发生"。
func TestConnections_EgressScopeTextFollowsScanWindow(t *testing.T) {
	f := newFixture(t, nil)
	if s := f.a.ConnectionView("").EgressScope; !strings.Contains(s, "还没有任何出网留痕") {
		t.Errorf("无留痕时的范围文案 = %q", s)
	} else if strings.Contains(s, "--audit") {
		t.Errorf("审计没落盘却让人去查落盘审计：%q", s)
	}

	f.a.Gate.RecordEgress("llm", "api.example.com", 10)
	if s := f.a.ConnectionView("").EgressScope; !strings.Contains(s, "本次运行内存里") {
		t.Errorf("有留痕但未写满时应说明只数了本次运行：%q", s)
	}

	f.a.Gate.SetAuditPath(filepath.Join(f.a.Cfg.DataDir, "audit.jsonl"))
	if s := f.a.ConnectionView("").EgressScope; !strings.Contains(s, "--audit --egress") {
		t.Errorf("落了盘就该给出回看入口：%q", s)
	}
	f.a.Gate.SetAuditPath("")
	if s := f.a.ConnectionView("").EgressScope; strings.Contains(s, "--audit") {
		t.Errorf("落盘撤掉后指路必须跟着撤：%q", s)
	}
}

// TestConnections_EgressScopeAdmitsRingOverflow 环写满时必须换口径。
// 这条测的是"多报"：读数只有最近 200 条，说成"本次运行的全部"就是假账。
func TestConnections_EgressScopeAdmitsRingOverflow(t *testing.T) {
	f := newFixture(t, nil)
	for i := 0; i < 240; i++ {
		f.a.Gate.RecordEgress("llm", "api.example.com", 1)
	}
	led := f.a.ConnectionView("")
	if !strings.Contains(led.EgressScope, "环已写满") {
		t.Errorf("留痕溢出仍说\"本次运行\"：%q", led.EgressScope)
	}
	// 溢出后单行读数也受影响：主机去重只数了环里那些，所以范围文案必须同处出现。
	r, _ := rowByID(led, "llm")
	if !strings.Contains(r.Stats, "已出网") {
		t.Errorf("溢出时模型行该有读数：%q", r.Stats)
	}
}

// TestConnections_MarketRowIsNotAConnection 市场这一行存在的意义是说"这不是连接"。
// 它是全表唯一一条反方向的交代：用户以为在联网，其实一次请求都不发。
func TestConnections_MarketRowIsNotAConnection(t *testing.T) {
	f := newFixture(t, nil)
	r, ok := rowByID(f.a.ConnectionView(""), "market")
	if !ok {
		t.Fatal("缺市场行")
	}
	if r.Kind != "local" {
		t.Errorf("市场不是网络边界，kind = %q", r.Kind)
	}
	if !strings.Contains(r.Leaves, "不外发") || !strings.Contains(r.Status, "离线") {
		t.Errorf("市场行必须说清它不发包：status=%q leaves=%q", r.Status, r.Leaves)
	}
	if r.Stats != "" {
		t.Errorf("不发东西的行不该有出网读数：%q", r.Stats)
	}
}
