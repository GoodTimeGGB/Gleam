package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// ---------- 大输出预算（工具输出注入下游前先处理） ----------

// TestFitRunes_KeepsHeadAndTail 超长文本按「头 + 尾 + 省略说明」截断，并明确告知被截断。
func TestFitRunes_KeepsHeadAndTail(t *testing.T) {
	head := strings.Repeat("甲", 400)
	tail := strings.Repeat("乙", 400)
	src := head + strings.Repeat("丙", 5000) + tail

	got := (&Executor{MaxOutputRunes: 1000}).fitRunes(src, "t1", "s1")
	if len([]rune(got)) > 1100 {
		t.Fatalf("截断后仍过长：%d 字", len([]rune(got)))
	}
	if !strings.Contains(got, "已截断") || !strings.Contains(got, "5800") {
		t.Errorf("应说明被截断及原始长度，实际片段：%s", got[len(got)-200:])
	}
	if !strings.HasPrefix(got, head) {
		t.Error("应保留开头")
	}
	// 结尾断言按「源文本的末尾」比，而不是按某个固定的保留长度：
	// 保留多少由预算减去提示语长度决定（提示语里含一条可能很长的落盘路径），
	// 钉死长度会让这条测试在提示语措辞变化时莫名其妙地红。
	if !strings.HasSuffix(got, string([]rune(tail)[100:])) {
		t.Error("应保留结尾")
	}
}

// TestFitRunes_ShortUnchanged 未超预算的文本必须原样返回，不能无谓改动。
func TestFitRunes_ShortUnchanged(t *testing.T) {
	src := "很短的一段输出"
	if got := (&Executor{MaxOutputRunes: 6000}).fitRunes(src, "t1", "s1"); got != src {
		t.Fatalf("应原样返回，实际 %q", got)
	}
}

// TestCapOutput_TypesPreserved 没超限时不改变类型——小结果必须原样透传（很多工具依赖结构化输出）。
func TestCapOutput_TypesPreserved(t *testing.T) {
	small := map[string]any{"n": 1, "entries": []any{"a", "b"}}
	got := (&Executor{MaxOutputRunes: 6000}).capOutput(small, "t1", "s1")
	if _, ok := got.(map[string]any); !ok {
		t.Errorf("小结果应保持原始类型，实际 %T", got)
	} else if !reflect.DeepEqual(got, small) {
		t.Errorf("小结果应原样返回，实际 %#v", got)
	}
	if got := (&Executor{MaxOutputRunes: 0}).capOutput("abc", "t1", "s1"); got != "abc" {
		t.Errorf("预算为 0 时不应改动，实际 %#v", got)
	}
}

// TestCapOutput_LargeStructBecomesTruncatedText 超限的结构化输出退化为截断文本，而不是整坨灌下去。
func TestCapOutput_LargeStructBecomesTruncatedText(t *testing.T) {
	entries := make([]any, 0, 500)
	for i := 0; i < 500; i++ {
		entries = append(entries, strings.Repeat("数据", 20))
	}
	got, ok := (&Executor{MaxOutputRunes: 1000}).capOutput(map[string]any{"entries": entries}, "t1", "s1").(string)
	if !ok {
		t.Fatal("超限的结构化输出应退化为字符串")
	}
	if len([]rune(got)) > 1100 {
		t.Fatalf("仍过长：%d 字", len([]rune(got)))
	}
	if !strings.Contains(got, "已截断") {
		t.Error("应标记已截断，避免模型误以为看到全貌")
	}
}

// TestExecutor_RefSubstitutionCapped 引用替换注入下游参数时必须受输出预算约束。
func TestExecutor_RefSubstitutionCapped(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "bigout", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, _ map[string]any) (any, error) {
			return strings.Repeat("很长的文件内容", 2000), nil // 约 1.4 万字
		}})
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, args map[string]any) (any, error) {
			return map[string]any{"text": args["text"]}, nil
		}})

	e := &Executor{
		Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, StepTimeout: 2 * time.Second,
		MaxOutputRunes: 1000,
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "bigout"},
		{ID: "s2", Tool: "reply", Args: map[string]any{"text": "内容：{ref:s1}"}, DependsOn: []string{"s1"}},
	}}
	res := e.Execute(context.Background(), plan, "t1", string(types.ModeAuto), false)
	if res.Succeeded != 2 {
		t.Fatalf("两步都应成功: %+v", res.Steps)
	}
	text, _ := res.ByID["s2"].FinalArgs["text"].(string)
	if got := len([]rune(text)); got > 1100 {
		t.Fatalf("注入下游的参数应被截断，实际 %d 字", got)
	}
	if !strings.Contains(text, "已截断") {
		t.Error("下游参数里应带截断说明，否则模型会以为拿到了全文")
	}
	// 上游步骤自身的结果不截断：用户要看的是完整输出
	full, _ := res.ByID["s1"].Output.(string)
	if len([]rune(full)) < 10000 {
		t.Errorf("步骤原始输出不应被裁剪，实际 %d 字", len([]rune(full)))
	}
}

// TestExecutor_RefSubstitutionUncappedWhenUnlimited 预算为 0（不限）时保持原样。
func TestExecutor_RefSubstitutionUncappedWhenUnlimited(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "bigout", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, _ map[string]any) (any, error) {
			return strings.Repeat("长内容", 3000), nil
		}})
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, args map[string]any) (any, error) {
			return map[string]any{"text": args["text"]}, nil
		}})
	e := &Executor{
		Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, StepTimeout: 2 * time.Second,
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "bigout"},
		{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{ref:s1}"}, DependsOn: []string{"s1"}},
	}}
	res := e.Execute(context.Background(), plan, "t2", string(types.ModeAuto), false)
	text, _ := res.ByID["s2"].FinalArgs["text"].(string)
	if strings.Contains(text, "已截断") {
		t.Error("预算为 0 时不应截断")
	}
}
