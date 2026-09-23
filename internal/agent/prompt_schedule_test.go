package agent

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"gleam/internal/llm"
	"gleam/internal/tools/std"
)

// TestScheduleSectionIsCompact 定时段在**稳定段**里，每轮规划都要带上，必须保持精简。
//
// 上限不是拍脑袋定的：压缩前是 734 字节（4 条完整规则 + 5 个 when 示例），
// 压到约 190 字节；留 300 的余量给措辞微调，但一旦有人把细节写回四条规则，这里立刻红。
func TestScheduleSectionIsCompact(t *testing.T) {
	sec := renderRules(SlotSchedule, "work", "", "")
	if n := len(sec); n > 300 {
		t.Errorf("定时段膨胀到 %d 字节（上限 300）——细节该放 schedule.create 的说明里，别搬回稳定段", n)
	}
	// 标题是稳定段的定位锚点：就绪体检的 stablePromptMarkers 与缓存布局测试都按它找这一段。
	if !strings.Contains(sec, "## 定时/周期任务") {
		t.Error("段落标题不能改：就绪体检与缓存布局测试都按这个标记定位稳定段")
	}
	// 这一句是这段**唯一不可替代**的内容：工具描述能说清"这工具干什么"，
	// 说不出"别只口头答应"。删了它，模型可能只在回复里答应而不真正建任务。
	if !strings.Contains(sec, "不要只在回复里答应") {
		t.Error("「不要只在回复里答应」不能删——它是这段唯一不可替代的行为约束")
	}
}

// TestScheduleGuidanceNotLost 提示词减法的安全网：**信息只能搬家，不能丢**。
//
// 从稳定段删掉的细节（when 的写法、name/goal 的要求、创建后如何确认），
// 必须仍能在 schedule.create 自己的 description / schema 里找到——
// 那两处会随被选中的工具一起进入同一次请求，模型照样看得到。
//
// 断言刻意写成「两处的并集」而不是「指定位置」：以后再搬一次也不用改测试，
// 但只要有一句话在两处都消失，测试立刻红。
func TestScheduleGuidanceNotLost(t *testing.T) {
	tool := std.NewScheduleCreate(nil)
	visible := renderRules(SlotSchedule, "work", "", "") + "\n" + tool.Description() + "\n" + fmt.Sprint(tool.Schema())

	for _, want := range []struct{ what, needle string }{
		{"触发条件「每天」", "每天"},
		{"触发条件「到点提醒我」", "到点提醒我"},
		{"行为约束「不要只在回复里答应」", "不要只在回复里答应"},
		{"时间放 when", "when"},
		{"不要自己拼 cron", "不要自己拼 cron"},
		{"name 必填", "name"},
		{"goal 必填", "goal"},
		{"创建后用 reply 确认", "reply"},
	} {
		if !strings.Contains(visible, want.needle) {
			t.Errorf("%s（%q）在提示词段和工具说明里都找不到——减法把信息弄丢了", want.what, want.needle)
		}
	}
}

// TestScheduleDetailStaysOutOfMenu 钉住一个容易忽略的耦合：
// 工具描述的**头 60 字符**会经 toolMenuSection → firstLine 截断后进入「能力菜单」，
// 而能力菜单属于**稳定段**。所以往工具描述里加细节时，必须保证它落在 60 字之后——
// 否则细节又绕回稳定段，这次减法等于白做。
func TestScheduleDetailStaysOutOfMenu(t *testing.T) {
	line := firstLine(std.NewScheduleCreate(nil).Description())
	// firstLine → types.Shorten(s, 60)：截 60 字后再补一个省略号，所以上限是 61 字不是 60。
	if n := utf8.RuneCountInString(line); n > 61 {
		t.Errorf("菜单行 %d 字，超出「60 字 + 省略号」的约定", n)
	}
	for _, detail := range []string{"到点提醒我", "不要自己拼 cron", "reply", "name 取简短"} {
		if strings.Contains(line, detail) {
			t.Errorf("细节 %q 混进了能力菜单（稳定段）——等于没做减法", detail)
		}
	}
	// 但"这工具是干什么的"必须留在菜单里，否则模型看菜单都不知道它存在。
	if !strings.Contains(line, "定时") {
		t.Error("菜单行应保留「定时」，否则模型无法从菜单发现这个工具")
	}
}

// TestScheduleSectionStaysInStablePrefix 压缩不能把这一段挤出稳定段。
// 它必须排在易变段之前，否则前缀缓存会从它开始失效。
func TestScheduleSectionStaysInStablePrefix(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 12, Role: "analyst", TaskMode: "work", Style: "efficient"}
	a := p.buildSystemPrompt("把上季度销售数据整理成表格并汇总", "D:/ws", nil, nil, "", "")
	b := p.buildSystemPrompt("帮我写一封给客户的道歉邮件", "D:/ws/sub", nil, nil, "", "")

	stable, _, _ := llm.SplitCacheBoundary(a, b)
	if !strings.Contains(stable, "## 定时/周期任务") {
		t.Error("定时段应仍落在跨请求公共前缀内")
	}
	if !strings.Contains(stable, "不要只在回复里答应") {
		t.Error("定时段的行为约束应仍落在公共前缀内")
	}
}
