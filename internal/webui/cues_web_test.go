package webui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/pkg/types"
)

// 候补目标的判据一律走**真实路由**。引擎层测的是「算得出这几张卡」，这一层要挡的失效是
// 路由没注册、handler 把空视图返回给界面、处置端点写了盘却没让卡消失——纯函数测试一律绿。

func cueRun(taskID, goal, trace string, status types.GoalStatus, errKind map[types.ErrorKind]int) *types.GoalResult {
	start := time.Now().Add(-2 * time.Hour)
	return &types.GoalResult{
		TaskID: taskID, Goal: goal, Status: status, TraceID: trace, Score: 15,
		FailureBreakdown: errKind, StartedAt: start, FinishedAt: start.Add(3 * time.Minute),
	}
}

// seedCueHistory 落两份同一目标的失败归档。TraceID 相同正是「同一个问题反复出现」的依据。
//
// 每份只记 1 步失败，故意压在 tool_failure 的门槛（累计 3 步）之下：这一层要盯的是
// 「一张卡进出」的接线，两张卡会让"消失/回来"的计数说不清到底滤掉了哪一张。
// 多条线索同时够格时的取舍由引擎层 TestCues_RepeatedFailureWinsOverHalfDone 那组负责。
func seedCueHistory(t *testing.T, f *fixture) {
	t.Helper()
	for i, id := range []string{"cue-1", "cue-2"} {
		g := cueRun(id, "把剪辑好的素材归档", "traceRepeat", types.GoalFailed, map[types.ErrorKind]int{types.ErrPermission: 1})
		g.StartedAt = g.StartedAt.Add(time.Duration(i) * time.Hour)
		g.FinishedAt = g.FinishedAt.Add(time.Duration(i) * time.Hour)
		if err := agent.SaveTaskResult(f.dataDir, g); err != nil {
			t.Fatalf("落归档失败：%v", err)
		}
	}
}

func cueRows(t *testing.T, f *fixture) []map[string]any {
	t.Helper()
	out := f.call("GET", "/api/cues", nil)
	raw, _ := out["rows"].([]any)
	rows := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	return rows
}

// TestCues_RouteServesDerivedRows 走路由能拿到卡，且每张卡带着「凭什么提这个」。
func TestCues_RouteServesDerivedRows(t *testing.T) {
	f := newFixture(t, nil)
	if rows := cueRows(t, f); len(rows) != 0 {
		t.Fatalf("空历史不该有候补卡，实得 %d 张", len(rows))
	}
	seedCueHistory(t, f)

	rows := cueRows(t, f)
	if len(rows) == 0 {
		t.Fatal("反复失败的历史浮不出一张卡（路由或派生断了）")
	}
	first := rows[0]
	if s, _ := first["signal"].(string); s != "repeat_failure" {
		t.Errorf("这份历史应浮出「同一个目标反复失败」，实得 %v", first["signal"])
	}
	for _, key := range []string{"id", "signal", "signal_text", "title", "goal", "why", "last_seen"} {
		if s, _ := first[key].(string); s == "" {
			t.Errorf("候补卡缺字段 %q：%v", key, first)
		}
	}
	if ev, _ := first["evidence"].([]any); len(ev) == 0 {
		t.Error("候补卡没有现场，用户没法核对它说的是不是真的")
	}
	led := f.call("GET", "/api/cues", nil)
	if cov, _ := led["coverage"].(string); cov == "" {
		t.Error("候补页没交代这张表的覆盖范围")
	}
}

// TestCues_DismissAdoptAndUnsuppressRoundTrip 处置要真的让卡消失，而「别再提」要能反悔。
func TestCues_DismissAdoptAndUnsuppressRoundTrip(t *testing.T) {
	f := newFixture(t, nil)
	seedCueHistory(t, f)
	rows := cueRows(t, f)
	if len(rows) != 1 {
		t.Fatalf("这份历史只该浮一张卡，实得 %d 张（种子失效，下面的计数就没意义了）", len(rows))
	}
	id, _ := rows[0]["id"].(string)

	f.call("POST", "/api/cues/"+id+"/dismiss", nil)
	if rows := cueRows(t, f); len(rows) != 0 {
		t.Errorf("记了别再提之后仍有 %d 张卡", len(rows))
	}
	led := f.call("GET", "/api/cues", nil)
	sup, _ := led["suppressed"].([]any)
	if len(sup) != 1 {
		t.Fatalf("处置记录没进 suppressed：%v", led["suppressed"])
	}
	entry, _ := sup[0].(map[string]any)
	if r, _ := entry["reason"].(string); r != "dismissed" {
		t.Errorf("处置原因应为 dismissed，实得 %v", r)
	}

	code, _ := f.raw("POST", "/api/cues/unsuppress", map[string]any{"fingerprint": id})
	if code != 200 {
		t.Fatalf("撤销处置应 200，实得 %d", code)
	}
	if rows := cueRows(t, f); len(rows) != 1 {
		t.Errorf("撤销之后那张卡应该回来，实得 %d 张", len(rows))
	}

	f.call("POST", "/api/cues/"+id+"/adopt", nil)
	if rows := cueRows(t, f); len(rows) != 0 {
		t.Errorf("采纳之后这张卡不该再浮出来，实得 %d 张", len(rows))
	}
}

// TestCues_AdoptDoesNotStartAnything 采纳只改状态：这一层没有手。
//
// 判据是盘上的文件集合——真跑起来会留下运行日志、任务归档、快照目录，
// 那些比任何字段断言都硬。
func TestCues_AdoptDoesNotStartAnything(t *testing.T) {
	f := newFixture(t, nil)
	seedCueHistory(t, f)
	id, _ := cueRows(t, f)[0]["id"].(string)

	before := cueTree(t, f.dataDir)
	f.call("POST", "/api/cues/"+id+"/adopt", nil)
	after := cueTree(t, f.dataDir)

	var added []string
	for _, p := range after {
		if !contains(before, p) {
			added = append(added, p)
		}
	}
	if len(added) != 1 || added[0] != "cues.json" {
		t.Errorf("采纳之后盘上只该多出处置记录，实得 %v", added)
	}
}

// TestCues_UnknownOrStaleIDIs404 已经不成立的卡（历史被裁剪）处置时要报错，不许静默成功。
func TestCues_UnknownOrStaleIDIs404(t *testing.T) {
	f := newFixture(t, nil)
	if code, _ := f.raw("POST", "/api/cues/no-such-cue/dismiss", nil); code != 404 {
		t.Errorf("处置一张不存在的卡应 404，实得 %d", code)
	}
	if code, _ := f.raw("POST", "/api/cues/unsuppress", map[string]any{"fingerprint": "no-such"}); code != 404 {
		t.Errorf("撤销一条不存在的处置应 404，实得 %d", code)
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func cueTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
}
