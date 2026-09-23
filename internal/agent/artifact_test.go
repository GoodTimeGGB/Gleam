package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gleam/pkg/types"
)

// ---------- 验收的确定性层：产物核对 ----------

// writeStep 造一个「file.write 自述成功」的步骤。
// Output 里带工具真正返回的已解析路径（真实链路就是这样，所以核对不必自己解析相对路径）。
func writeStep(id, path, content string, appendMode bool) types.StepResult {
	args := map[string]any{"path": path, "content": content}
	if appendMode {
		args["append"] = true
	}
	return types.StepResult{
		StepID: id, Tool: "file.write", Status: types.StepSucceeded,
		Output:    map[string]any{"path": path, "bytes_written": len(content), "append": appendMode},
		FinalArgs: args,
	}
}

func TestVerifyArtifacts_MissingFileIsCaught(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e2e.txt")
	facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, "内容", false)})
	if len(facts) != 1 {
		t.Fatalf("应产生 1 条事实，实际 %d", len(facts))
	}
	if facts[0].ok {
		t.Errorf("文件根本不存在，不该判通过：%+v", facts[0])
	}
	if !strings.Contains(facts[0].detail, "不存在") {
		t.Errorf("说明应点出文件不存在，实际 %q", facts[0].detail)
	}
}

// 路径对、文件也在，但只写进去一部分——只看"存在"发现不了，必须比长度。
func TestVerifyArtifacts_LengthMismatchIsCaught(t *testing.T) {
	p := filepath.Join(t.TempDir(), "half.txt")
	if err := os.WriteFile(p, []byte("一半"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "完整内容应该是这么长的一段文字"
	facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, content, false)})
	if facts[0].ok {
		t.Errorf("长度不符不该判通过：%+v", facts[0])
	}
	if !strings.Contains(facts[0].detail, "长度不符") {
		t.Errorf("说明应点出长度不符，实际 %q", facts[0].detail)
	}
}

func TestVerifyArtifacts_MatchIsOK(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ok.txt")
	content := "写进去的内容"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, content, false)})
	if !facts[0].ok {
		t.Errorf("内容与长度都对，应判通过：%+v", facts[0])
	}
}

// 追加写无法断言精确长度（文件可能本来就有内容），只能卡下界。
func TestVerifyArtifacts_AppendChecksLowerBound(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(p, []byte("旧的"), 0o644); err != nil { // 6 字节
		t.Fatal(err)
	}
	if facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, "新增", true)}); !facts[0].ok {
		t.Errorf("追加后长度足够，应判通过：%+v", facts[0])
	}
	long := strings.Repeat("长", 100) // 300 字节，远超文件实际长度
	if facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, long, true)}); facts[0].ok {
		t.Errorf("文件比追加内容还短，不该判通过：%+v", facts[0])
	}
}

// 同一路径写两次只留最后一次：逐条判会把"被后续步骤覆盖"误报成"产物丢失"。
func TestVerifyArtifacts_LaterWriteSupersedes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.txt")
	final := "第二次写的内容"
	if err := os.WriteFile(p, []byte(final), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := verifyArtifacts([]types.StepResult{
		writeStep("s1", p, "第一次写的内容", false), // 长度与最终状态不同
		writeStep("s2", p, final, false),
	})
	if len(facts) != 1 {
		t.Fatalf("同一路径应只留一条最终期望，实际 %d：%+v", len(facts), facts)
	}
	if !facts[0].ok {
		t.Errorf("应只按最后一次写判定：%+v", facts[0])
	}
	if facts[0].stepID != "s2" {
		t.Errorf("应归到最后一次写的步骤，实际 %s", facts[0].stepID)
	}
}

// 写完又删掉是正常流程，不该报"产物丢失"——假警报会让这道闸被当成噪音关掉。
func TestVerifyArtifacts_WriteThenDeleteIsNotAFalseAlarm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tmp.txt")
	facts := verifyArtifacts([]types.StepResult{
		writeStep("s1", p, "临时内容", false),
		{StepID: "s2", Tool: "file.delete", Status: types.StepSucceeded,
			Output: map[string]any{"path": p, "deleted": true}},
	})
	if len(facts) != 1 {
		t.Fatalf("应只留一条最终期望（不存在），实际 %d：%+v", len(facts), facts)
	}
	if !facts[0].ok {
		t.Errorf("写完又删掉不该判失败：%+v", facts[0])
	}
}

func TestVerifyArtifacts_Mkdir(t *testing.T) {
	d := filepath.Join(t.TempDir(), "sub")
	mk := []types.StepResult{{StepID: "s1", Tool: "file.mkdir", Status: types.StepSucceeded,
		Output: map[string]any{"path": d, "created": true}}}
	if facts := verifyArtifacts(mk); len(facts) != 1 || facts[0].ok {
		t.Errorf("目录不存在，不该判通过：%+v", facts)
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if facts := verifyArtifacts(mk); !facts[0].ok {
		t.Errorf("目录已建好，应判通过：%+v", facts[0])
	}
}

// 移动有两个事后条件，缺一不可：只查目标会漏掉"复制成两份"，只查源会漏掉"搬丢了"。
func TestVerifyArtifacts_MoveChecksBothEnds(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	move := []types.StepResult{{StepID: "s1", Tool: "file.move", Status: types.StepSucceeded,
		Output: map[string]any{"src": src, "dst": dst}}}

	for _, p := range []string{src, dst} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	facts := verifyArtifacts(move)
	if len(facts) != 2 {
		t.Fatalf("移动应有两个事后条件，实际 %d：%+v", len(facts), facts)
	}
	if artifactsSatisfied(facts) {
		t.Errorf("源还在（只是复制了一份），不该判通过：%+v", facts)
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if facts := verifyArtifacts(move); !artifactsSatisfied(facts) {
		t.Errorf("源已消失、目标在，应判通过：%+v", facts)
	}
}

// 没跑成的写、以及不产生产物的工具都不该产生事实：那是三态/错误层的事，不在这里重复记账。
func TestVerifyArtifacts_IgnoresFailedStepsAndUnknownTools(t *testing.T) {
	dir := t.TempDir()
	steps := []types.StepResult{
		{StepID: "s1", Tool: "file.write", Status: types.StepFailed, Error: "权限不足",
			FinalArgs: map[string]any{"path": filepath.Join(dir, "nope.txt"), "content": "x"}},
		{StepID: "s2", Tool: "file.list", Status: types.StepSucceeded,
			Output: map[string]any{"path": dir, "count": 0}},
		{StepID: "s3", Tool: "shell.exec", Status: types.StepSucceeded,
			Output: map[string]any{"exit_code": 0}},
	}
	if facts := verifyArtifacts(steps); len(facts) != 0 {
		t.Errorf("这些步骤都不该产生产物事实，实际 %+v", facts)
	}

	// 失败步骤**即使带着输出**也不进入产物核对（StepResult 允许「失败 + 有输出」这种组合，
	// 工具完全可以 `return out, err`）。当前执行器只在 err==nil 时记 Output，所以这条
	// 看起来永远不会触发——但它守的是一条契约：失败由三态/错误层记账，
	// 这里再记一次就会变成两个"不通过"，把一处失败放大成两处。
	failedWithOutput := types.StepResult{
		StepID: "s4", Tool: "file.write", Status: types.StepFailed, Error: "写到一半失败",
		Output:    map[string]any{"path": filepath.Join(dir, "half.txt")},
		FinalArgs: map[string]any{"path": filepath.Join(dir, "half.txt"), "content": "x"},
	}
	if facts := verifyArtifacts([]types.StepResult{failedWithOutput}); len(facts) != 0 {
		t.Errorf("未成功的步骤不该进入产物核对，实际 %+v", facts)
	}

	if !artifactsSatisfied(nil) {
		t.Error("没有可核对的产物时应放行（不干预原有流程）")
	}
	if artifactDigest(nil) != "" {
		t.Error("没有事实时不该产生摘要段落")
	}
}

func TestArtifactDigest_ReportsFailures(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.txt")
	sec := artifactDigest(verifyArtifacts([]types.StepResult{writeStep("s1", p, "内容", false)}))
	if !strings.Contains(sec, "[不通过]") || !strings.Contains(sec, p) {
		t.Errorf("摘要应标出不通过项与路径：\n%s", sec)
	}
	if !strings.Contains(sec, "自述成功") {
		t.Errorf("摘要应点明「自述成功不等于产物在」：\n%s", sec)
	}
}

// 反思器说 done/98，但代码实测产物不在——不许判 done。
func TestEvaluate_ArtifactMismatchForcesReplan(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.txt")
	exec := &Result{Total: 1, Succeeded: 1}
	exec.Steps = []types.StepResult{writeStep("s1", p, "内容", false)}
	r := &Reflector{LLM: mockReturning(`{"score":98,"verdict":"done","reason":"全部完成"}`)}
	refl := r.Evaluate(context.Background(), "写个文件", exec, 1)
	if refl.Verdict == "done" {
		t.Errorf("产物核对不过时不许判 done，实际 %q", refl.Verdict)
	}
	if !strings.Contains(refl.Reason, "产物核对未通过") {
		t.Errorf("原因里应说明产物核对，实际 %q", refl.Reason)
	}
}

// 没有验收标准的任务同样会「步骤都报成功、产物其实不在」。
// statusOfExec 只看步骤计数，会把它判成 GoalSuccess——这里就是那道最后闸门。
func TestApplyAcceptanceVerdict_ArtifactMismatchWithoutCriteria(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.txt")
	res := &types.GoalResult{
		Status: types.GoalSuccess, Score: 90,
		Steps: []types.StepResult{writeStep("s1", p, "内容", false)},
	}
	applyAcceptanceVerdict(res, nil) // 一条验收标准都没有
	if res.Status != types.GoalPartial {
		t.Errorf("产物实测不在就不许声称完成，实际状态 %v", res.Status)
	}
	if !strings.Contains(res.Error, "产物核对未通过") {
		t.Errorf("应写明原因，实际 %q", res.Error)
	}
}

// 最要紧的一条：验收标准自己判了"通过"，但代码实测产物不在。
// 这正是「摘要看着对、产物其实错」——语义层判过不算数，确定性层说了算。
func TestApplyAcceptanceVerdict_ArtifactBeatsPassedCriteria(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.txt")
	acc := []string{"文件已创建"}
	res := &types.GoalResult{
		Status: types.GoalSuccess, Score: 100,
		Acceptance: acc,
		Checks:     []types.CheckResult{{Criterion: "文件已创建", Passed: true}},
		Steps:      []types.StepResult{writeStep("s1", p, "内容", false)},
	}
	applyAcceptanceVerdict(res, acc)
	if res.Status != types.GoalPartial {
		t.Errorf("标准判过但产物实测不在，仍不许声称完成，实际状态 %v", res.Status)
	}
}

// 对照组：产物核对通过时不许乱动状态。
func TestApplyAcceptanceVerdict_ArtifactOKKeepsSuccess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ok.txt")
	content := "写好的内容"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res := &types.GoalResult{
		Status: types.GoalSuccess, Score: 90,
		Steps: []types.StepResult{writeStep("s1", p, content, false)},
	}
	applyAcceptanceVerdict(res, nil)
	if res.Status != types.GoalSuccess {
		t.Errorf("产物核对通过时不该改动状态，实际 %v", res.Status)
	}
}

// 循环的完成判定：产物核对不过时必须拦住**分数达标**那半边。
// 反思器已经被压成 replan，但 score=98 ≥ 阈值，只看前两条照样会放行——
// 这正是这个守卫承重的地方。
func TestCompletionReached_ArtifactMismatchBlocks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.txt")
	bad := &Result{Total: 1, Succeeded: 1, Steps: []types.StepResult{writeStep("s1", p, "内容", false)}}
	refl := types.Reflection{Score: 98, Verdict: "replan"}
	if completionReached(refl, nil, bad, 80) {
		t.Error("产物核对不过时不许判完成（分数达标的半边会放行，必须独立拦住）")
	}

	okPath := filepath.Join(t.TempDir(), "ok.txt")
	if err := os.WriteFile(okPath, []byte("内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := &Result{Total: 1, Succeeded: 1, Steps: []types.StepResult{writeStep("s1", okPath, "内容", false)}}
	if !completionReached(refl, nil, good, 80) {
		t.Error("产物核对通过时应正常判完成")
	}
	if completionReached(types.Reflection{Score: 10, Verdict: "replan"}, nil, good, 80) {
		t.Error("分数不达标、verdict 也不是 done，不该判完成")
	}
}

// ---------- 格式合法：只看"在不在、多长"发现不了"写进去的是坏 JSON" ----------

// putFile 按声明的内容原样落盘（长度因此与 writeStep 的期望一致，判定才能走到格式那一档）。
func putFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyArtifacts_BrokenJSONIsCaught(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data.json")
	content := `{"a":1,}` // 尾逗号：长度对得上，但不是合法 JSON
	putFile(t, p, content)

	facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, content, false)})
	if facts[0].ok {
		t.Fatalf("坏掉的 JSON 不该判通过：%+v", facts[0])
	}
	if !strings.Contains(facts[0].detail, "合法 JSON") {
		t.Errorf("说明应点出格式不合法，实际 %q", facts[0].detail)
	}
}

func TestVerifyArtifacts_ValidJSONPasses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ok.json")
	content := `{"a":1,"b":[2,3]}`
	putFile(t, p, content)
	if facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, content, false)}); !facts[0].ok {
		t.Errorf("合法 JSON 应判通过：%+v", facts[0])
	}
}

// JSONL 常被写成 .json：整体解析不通、但每行都是合法 JSON，这不算写坏。
func TestVerifyArtifacts_JSONLTolerated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rows.json")
	content := "{\"a\":1}\n{\"b\":2}\n"
	putFile(t, p, content)
	if facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, content, false)}); !facts[0].ok {
		t.Errorf("逐行合法的 JSONL 不该被判写坏：%+v", facts[0])
	}
}

func TestVerifyArtifacts_BOMTolerated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bom.json")
	content := "\ufeff{\"a\":1}"
	putFile(t, p, content)
	if facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, content, false)}); !facts[0].ok {
		t.Errorf("带 BOM 的合法 JSON 不该被判写坏：%+v", facts[0])
	}
}

// 追加写可能只是在往已有文件尾部贴一段，去判格式就是假警报——刻意不查。
func TestVerifyArtifacts_AppendJSONNotChecked(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.json")
	putFile(t, p, "上一轮写的内容")
	facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, "新追加的一段", true)})
	if !facts[0].ok {
		t.Errorf("追加写不判格式：%+v", facts[0])
	}
}

func TestVerifyArtifacts_NonJSONExtensionNotChecked(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.txt")
	content := "这不是 JSON，但 .txt 没有格式承诺"
	putFile(t, p, content)
	if facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, content, false)}); !facts[0].ok {
		t.Errorf(".txt 不该做 JSON 判定：%+v", facts[0])
	}
}

// 空内容不判：长度校验已经管了"该有内容却没有"，这里再报一次只是重复记账。
func TestVerifyArtifacts_EmptyJSONNotFlagged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.json")
	putFile(t, p, "")
	if facts := verifyArtifacts([]types.StepResult{writeStep("s1", p, "", false)}); !facts[0].ok {
		t.Errorf("空文件不该被当成格式错误：%+v", facts[0])
	}
}

// 格式不合法要能一路走到闸门，而不是只停在 artifactFact 上的一条记录。
func TestApplyAcceptanceVerdict_BrokenJSONBlocks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.json")
	content := `{"broken":`
	putFile(t, p, content)
	res := &types.GoalResult{
		Status: types.GoalSuccess,
		Steps:  []types.StepResult{writeStep("s1", p, content, false)},
	}
	applyAcceptanceVerdict(res, nil) // 连验收标准都没有，也必须拦住
	if res.Status != types.GoalPartial {
		t.Errorf("格式不合法的产物不该以 success 收尾，实际 %v", res.Status)
	}
	if !strings.Contains(res.Error, "合法 JSON") {
		t.Errorf("对外说明应点出格式问题，实际 %q", res.Error)
	}
}
