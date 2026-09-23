package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 通知策略的判据只有一份实现（Job.ShouldNotify）。这里把它的边界钉住。
//
// 断言顺序有意为之：**先证"该响的时候响了"，再证"该静默的时候静默"**。
// 反过来写，"没响"在判据压根没被调用时也成立——用错误的理由通过比直接失败更坏。
func TestShouldNotify_PolicyMatrix(t *testing.T) {
	cases := []struct {
		name      string
		policy    string
		succeeded bool
		want      bool
	}{
		// ---- 正例：默认策略下失败必须响。整套东西存在的理由就是这一条 ----
		{"默认·失败要响", "", false, true},
		{"always·失败要响", NotifyAlways, false, true},
		{"on_failure·失败要响", NotifyOnFailure, false, true},
		{"脏值·失败仍然要响", "sometimes", false, true},
		// ---- 反例：该静默的时候静默 ----
		{"默认·成功静默", "", true, false},
		{"on_failure·成功静默", NotifyOnFailure, true, false},
		{"never·成功静默", NotifyNever, true, false},
		{"never·失败也静默（用户显式要求）", NotifyNever, false, false},
		{"脏值·成功静默", "sometimes", true, false},
		// ---- 正例：显式 always 连成功也要响 ----
		{"always·成功也响", NotifyAlways, true, true},
		{"带空白仍然认得出", "  always  ", true, true},
	}
	for _, c := range cases {
		j := Job{Name: "j", Notify: c.policy}
		if got := j.ShouldNotify(c.succeeded); got != c.want {
			t.Errorf("%s: ShouldNotify(%v) = %v，应为 %v", c.name, c.succeeded, got, c.want)
		}
	}
}

func TestNormalizeNotify(t *testing.T) {
	ok := map[string]string{
		"":              NotifyOnFailure,
		"   ":           NotifyOnFailure,
		"always":        NotifyAlways,
		"on_failure":    NotifyOnFailure,
		"never":         NotifyNever,
		"  always\t":    NotifyAlways,
		" on_failure  ": NotifyOnFailure,
	}
	for in, want := range ok {
		got, err := NormalizeNotify(in)
		if err != nil || got != want {
			t.Errorf("NormalizeNotify(%q) = (%q, %v)，应为 %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"sometimes", "ALWAYS", "0", "true"} {
		if _, err := NormalizeNotify(bad); err == nil {
			t.Errorf("NormalizeNotify(%q) 应报错", bad)
		}
	}
}

// 默认策略**不落盘**：省掉一个字段，也让"没写过"与"写成了默认"不可区分——
// 而它们本来就该是同一件事。反过来，显式策略必须落盘并能被重新读出来。
func TestSetNotify_PersistsExplicitPolicyOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedules.json")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddJob("j", "0 9 * * *", 0, "巡检", "auto"); err != nil {
		t.Fatal(err)
	}

	if err := s.SetNotify("j", NotifyAlways); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotify("j", NotifyNever); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"notify": "never"`) && !strings.Contains(string(raw), `"notify":"never"`) {
		t.Errorf("显式策略应落盘，实际文件内容：%s", raw)
	}

	// 设回默认 → 字段从文件里消失
	if err := s.SetNotify("j", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "notify") {
		t.Errorf("默认策略不该落盘，实际文件内容：%s", raw)
	}

	// 非法策略：报错，且**不改变已有设置**
	if err := s.SetNotify("j", NotifyAlways); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotify("j", "sometimes"); err == nil {
		t.Error("非法策略应报错")
	}
	var jobs []Job
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &jobs); err != nil {
		t.Fatalf("读回调度文件失败：%v", err)
	}
	if len(jobs) != 1 || jobs[0].Notify != NotifyAlways {
		t.Errorf("非法策略不该改掉已有设置，实际：%+v", jobs)
	}
}

func TestSetNotify_MissingJob(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "schedules.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotify("nope", NotifyAlways); err == nil {
		t.Error("任务不存在时应报错")
	}
	// 任务不存在时**先校验策略**：非法策略同样报错，不能因为找不到任务就静默通过
	if err := s.SetNotify("nope", "sometimes"); err == nil {
		t.Error("非法策略应报错（即使任务不存在）")
	}
}
