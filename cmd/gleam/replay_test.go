package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/config"
	"gleam/pkg/types"
)

// 交付侧口径要在单任务复盘里看得见，而且三种情形必须能区分。
//
// 一次通过 / 返工 N 轮 / 没走到验收（两个字段都是零值，例如对话模式）
// 是三件不同的事——只印其中一种，另两种就混进来看不见了。
// 尤其不能把第三种印成"返工 0 轮（一次通过）"：那是在替一次没核对过的交付邀功。
func TestRenderTrace_DeliveryLine(t *testing.T) {
	cases := []struct {
		name string
		res  types.GoalResult
		want string // 空表示"不该出现返工行"
	}{
		{"一次通过", types.GoalResult{Status: types.GoalSuccess, FirstPass: true}, "返工：0 轮（一次通过）"},
		{"返工两轮", types.GoalResult{Status: types.GoalSuccess, Reworks: 2}, "返工：2 轮（非一次通过）"},
		{"没走到验收", types.GoalResult{Status: types.GoalCancelled}, ""},
	}
	for _, c := range cases {
		out := renderTrace(&c.res)
		if c.want == "" {
			if strings.Contains(out, "返工：") {
				t.Errorf("%s：不该印返工行，实际：\n%s", c.name, out)
			}
			continue
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%s：输出应包含 %q，实际：\n%s", c.name, c.want, out)
		}
	}
}

// 快照要在复盘输出里看得见，而且"没有快照"必须说出来。
//
// 旧任务文件里没有这一段——不印，读的人会把"没记录"当成"没改过配置"；
// 印成空白，读的人又会以为当时的配置就是空的。所以缺了要如实说缺了，
// 并点明后果：--rerun 会回退当前配置，差异不能全归给环境。
func TestRenderTrace_ConfigSnapshotLine(t *testing.T) {
	withSnap := types.GoalResult{
		Status: types.GoalSuccess,
		ConfigSnapshot: &types.ConfigSnapshot{
			Provider: "openai", Model: "gpt-x", MaxConcurrency: 4,
			StepTimeoutSecs: 45, DedupeCalls: true, MaxOutputRunes: 6000,
		},
	}
	out := renderTrace(&withSnap)
	for _, want := range []string{"配置快照：", "openai/gpt-x", "并发 4", "单步超时 45s", "去重 开", "输出预算 6000 字符"} {
		if !strings.Contains(out, want) {
			t.Errorf("快照行缺少 %q，实际：\n%s", want, out)
		}
	}

	noSnap := types.GoalResult{Status: types.GoalSuccess}
	out = renderTrace(&noSnap)
	if !strings.Contains(out, "配置快照：无") {
		t.Errorf("没有快照时必须如实说明，实际：\n%s", out)
	}
	if !strings.Contains(out, "回退当前配置") {
		t.Errorf("没有快照时要讲清 --rerun 会回退当前配置，否则差异会被误读，实际：\n%s", out)
	}
}

// 重跑优先用任务启动时的快照。
//
// 这是 P2-1 的核心：拿当前配置重跑，比出来的差异分不清是"环境变了"还是"配置变了"，
// 而复现实验的前提是只动一个变量。
func TestResolveRerunParams_PrefersSnapshotOverLive(t *testing.T) {
	snap := &types.ConfigSnapshot{
		MaxConcurrency: 2, StepTimeoutSecs: 11, StepRetries: 3,
		DedupeCalls: true, MaxOutputRunes: 700,
	}
	live := config.Default()
	live.Agent.MaxConcurrency = 64
	live.Agent.StepTimeoutSecs = 999
	live.Agent.StepRetries = 9
	live.Agent.DedupeCalls = false
	live.Agent.MaxOutputRunes = 1

	p := resolveRerunParams(snap, live)
	if !p.FromSnapshot {
		t.Error("有快照时 FromSnapshot 应为 true——否则输出里会说成回退，读的人会以为这次不是复现")
	}
	if p.MaxConcurrency != 2 || p.StepTimeout != 11*time.Second || p.StepRetries != 3 || !p.Dedupe || p.MaxOutputRunes != 700 {
		t.Errorf("没用快照的参数: %+v", p)
	}
	if !strings.Contains(p.Origin(), "快照") {
		t.Errorf("来源说明应点明取自快照，实际 %q", p.Origin())
	}
}

// 没有快照（本字段落地之前产出的任务文件）时回退当前配置，但必须**说出来**。
//
// 两条路都要能跑：直接拒绝重跑会让所有历史任务失去重跑能力，那是更差的取舍。
// 但回退不能被读成复现——"以为在复现、其实变量变了两个"是最坏的复盘姿势。
func TestResolveRerunParams_FallsBackToLiveConfig(t *testing.T) {
	live := config.Default()
	live.Agent.MaxConcurrency = 64
	live.Agent.StepTimeoutSecs = 999
	live.Agent.StepRetries = 9
	live.Agent.DedupeCalls = false
	live.Agent.MaxOutputRunes = 1

	p := resolveRerunParams(nil, live)
	if p.FromSnapshot {
		t.Error("没有快照时 FromSnapshot 应为 false")
	}
	if p.MaxConcurrency != 64 || p.StepTimeout != 999*time.Second || p.StepRetries != 9 || p.Dedupe || p.MaxOutputRunes != 1 {
		t.Errorf("回退值不对: %+v", p)
	}
	origin := p.Origin()
	if !strings.Contains(origin, "回退") {
		t.Errorf("来源说明必须点明是回退，实际 %q", origin)
	}
	if !strings.Contains(origin, "不能全归给环境") {
		t.Errorf("来源说明要点明后果（差异里混着配置变化），实际 %q", origin)
	}
}

// 两头都空也不能 panic：重跑入口拿到的可能是既没有快照、配置也没装配好的任务。
func TestResolveRerunParams_NilBoth(t *testing.T) {
	p := resolveRerunParams(nil, nil)
	if p.FromSnapshot || p.MaxConcurrency != 0 || p.StepTimeout != 0 {
		t.Errorf("应得零值参数，实际 %+v", p)
	}
}

// 接线断言：重跑的执行器必须真的吃到快照里的参数。
//
// 判据算对了不等于接上了——resolveRerunParams 写对、组装处却传了 nil 或活配置，
// 单元测试照样全绿，而每一次重跑都会拿今天的配置去比昨天的结果，
// 比出来的差异分不清是"环境变了"还是"配置变了"。这是 P4-2b 栽过的同一个坑。
func TestRerunExecutor_TakesParamsFromSnapshot(t *testing.T) {
	g := &types.GoalResult{
		Goal: "当时的任务",
		ConfigSnapshot: &types.ConfigSnapshot{
			MaxConcurrency: 2, StepTimeoutSecs: 11, StepRetries: 3,
			DedupeCalls: true, MaxOutputRunes: 700,
		},
	}
	live := config.Default()
	live.Agent.MaxConcurrency = 64
	live.Agent.StepTimeoutSecs = 999
	live.Agent.StepRetries = 9
	live.Agent.DedupeCalls = false
	live.Agent.MaxOutputRunes = 1

	ex, params := rerunExecutor(g.Goal, g.ConfigSnapshot, live, nil, nil, "", "")
	if !params.FromSnapshot {
		t.Error("有快照时来源应记成快照")
	}
	if ex.MaxConcurrency != 2 || ex.StepTimeout != 11*time.Second || ex.StepRetries != 3 || !ex.Dedupe || ex.MaxOutputRunes != 700 {
		t.Errorf("执行器没用快照的参数: %+v", ex)
	}
	if ex.Goal != "当时的任务" {
		t.Errorf("目标应原样带过去，实际 %q", ex.Goal)
	}

	// 没有快照的老任务：回退活配置，仍然要能跑。
	ex2, params2 := rerunExecutor("老任务", nil, live, nil, nil, "", "")
	if params2.FromSnapshot {
		t.Error("没有快照时应记成回退")
	}
	if ex2.MaxConcurrency != 64 || ex2.StepTimeout != 999*time.Second {
		t.Errorf("老任务应回退活配置: %+v", ex2)
	}
}

// 接线断言：回放必须接上运行日志。
//
// 回放记录是**跑完才写**的，一次跑到一半就没了的回放（用户关窗口、被强杀）
// 只可能靠运行日志留下来。这根线写在调用点的话，单测照样全绿，
// 而每次回放都不留任何运行中的凭据——"以为有据可查、其实没有"。
func TestRerunExecutor_WiresRunLogSink(t *testing.T) {
	dir := t.TempDir()
	ex, _ := rerunExecutor("任务", nil, config.Default(), nil, nil, dir, "replay-t1")
	if ex.Sink == nil {
		t.Fatal("回放执行器必须接上运行日志，否则中途退出什么都查不到")
	}
	ex.Sink.RecordStep("replay-t1", types.StepResult{StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK})
	if _, err := os.Stat(filepath.Join(dir, "runs", "replay-t1.jsonl")); err != nil {
		t.Errorf("运行日志应写在 runs/replay-t1.jsonl（与源任务分开）: %v", err)
	}
	// 没有数据目录时不接：不能凭空造一个路径出来。
	if ex2, _ := rerunExecutor("任务", nil, config.Default(), nil, nil, "", "x"); ex2.Sink != nil {
		t.Error("没有数据目录时不该接运行日志")
	}
}

// 快照行的两个细节：0 表示"不限"不能印成 0；档位顺序必须稳定。
//
// 前者印错比不印更坏——"输出预算 0 字符"会被读成"零预算"，与真实语义正好相反。
// 后者是 map 遍历顺序随机：同一份快照每次印得不一样，会被误读成"配置变了"。
func TestDescribeSnapshot_LimitLabelsAndStableOrder(t *testing.T) {
	s := &types.ConfigSnapshot{
		MaxOutputRunes: 0, MaxToolSchemas: 0, StepTimeoutSecs: 0,
		Tiers: map[string]string{"aa": "1", "bb": "2", "cc": "3", "dd": "4", "ee": "5"},
	}
	out := describeSnapshot(s)
	for _, want := range []string{"单步超时 不限", "输出预算 不限", "工具菜单 不限"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q，实际：%s", want, out)
		}
	}
	if strings.Contains(out, "输出预算 0") {
		t.Errorf("0 表示不限，不能印成 0：%s", out)
	}

	// 必须是升序，而不只是"每次都一样"：逆序也是稳定的，但两份快照仍然不可逐字比对。
	if strings.Index(out, "aa=") > strings.Index(out, "ee=") {
		t.Errorf("档位应按名字升序，实际：%s", out)
	}

	first := describeSnapshot(s)
	for i := 0; i < 20; i++ {
		if got := describeSnapshot(s); got != first {
			t.Fatalf("同一份快照印出了不同顺序（第 %d 次）——档位必须排序：\n%s\n%s", i, first, got)
		}
	}
}
