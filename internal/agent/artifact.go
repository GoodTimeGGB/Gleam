package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gleam/pkg/types"
)

// ---------- 验收的确定性层：产物核对 ----------
//
// 语言模型在反思时只能看到每步**截断 300 字的自述**（`<tool_result>` 里那段），
// 于是"说写了其实没写""写到别的路径去了""内容被截断了"这类事它看不出来——
// 而这类事恰好最容易发生，又最伤"完成"这个结论的可信度。
//
// 能确定性判定的部分就不该问模型（A3：精确计算归 Code）。这里做两件事：
//  1. 把代码实测到的事实写进反思器看到的材料——**把自述换成实测**；
//  2. 当作一道硬闸门：核对不过就不许声称完成（见 applyAcceptanceVerdict）。
//
// 刻意只做**确定性**的核对：文件系统上就能确认的事（在不在、是文件还是目录、长度对不对），
// 加上唯一一处读内容的例外——**格式合法**（.json 能不能解析，见 jsonBroken）。
// 语义判断（内容对不对、答得切不切题）留给语义层：混在一起会让这一层既慢又不可信。

const (
	wantFile   = "file"   // 应该是文件（可校验长度）
	wantDir    = "dir"    // 应该是目录
	wantAbsent = "absent" // 应该已经不存在（删除/移动的源）
	wantExists = "exists" // 存在即可，不限定类型
)

// artifactExpectation 一条产物的事后条件：某个路径**最终**应该处于什么状态。
type artifactExpectation struct {
	stepID  string
	tool    string
	path    string
	want    string
	exact   int // want=file 且 >=0：期望字节数（覆盖写）
	atLeast int // want=file 且 >=0：至少字节数（追加写）
	// jsonCheck want=file 且为覆盖写时，额外要求内容是一份能解析的 JSON。
	// 只看"文件在、长度对"发现不了"写进去的是半截/坏掉的 JSON"。
	jsonCheck bool
}

// artifactFact 代码实测的产物事实。
type artifactFact struct {
	stepID string
	tool   string
	path   string
	ok     bool
	detail string // 通过时写实测值；不通过时写差在哪
}

// verifyArtifacts 核对"声称产出了文件"的步骤。
//
// **按路径收敛、后写覆盖先写**，而不是逐条判步骤：同一条路径被写两次、或写完又删掉，
// 都是正常流程；逐条判会把"被后续步骤覆盖"误报成"产物丢失"，而假警报会让这道闸很快
// 被当成噪音关掉。只留每个路径的**最终**期望，才是在问正确的问题：
// "活干完了，说好的东西在不在？"
//
// 不认识的工具、拿不到路径的步骤一律跳过——**宁可不判，不可错判**。
func verifyArtifacts(steps []types.StepResult) []artifactFact {
	order := make([]string, 0, len(steps))
	final := map[string]artifactExpectation{}
	put := func(e artifactExpectation) {
		if e.path == "" {
			return
		}
		if _, seen := final[e.path]; !seen {
			order = append(order, e.path)
		}
		final[e.path] = e
	}

	for _, st := range steps {
		if st.Status != types.StepSucceeded {
			continue // 没跑成的步骤由三态/错误层负责，这里不重复记账
		}
		switch st.Tool {
		case "file.write":
			e := artifactExpectation{
				stepID: st.StepID, tool: st.Tool,
				path: outStr(st.Output, "path"), want: wantFile,
				exact: -1, atLeast: -1,
			}
			// 长度校验是这里最值钱的一条：路径对、文件也在，但只写进去一半
			// （编码问题、被截断、写到一半失败却报了成功）——只看"存在"发现不了。
			if content, ok := st.FinalArgs["content"].(string); ok {
				if argBool(st.FinalArgs, "append") {
					e.atLeast = len(content)
				} else {
					e.exact = len(content)
					// 只有覆盖写才谈得上"整份内容归我们负责"，也才谈得上格式合法。
					// 追加写可能只是在往已有文件尾部贴一段，去判它的格式就是假警报。
					e.jsonCheck = looksJSON(e.path)
				}
			}
			put(e)
		case "file.mkdir":
			put(artifactExpectation{
				stepID: st.StepID, tool: st.Tool,
				path: outStr(st.Output, "path"), want: wantDir,
				exact: -1, atLeast: -1,
			})
		case "file.move":
			// 移动有两个事后条件，缺一不可：源要没了、目标要在。
			// 只查目标会漏掉"复制成两份"，只查源会漏掉"搬丢了"。
			put(artifactExpectation{
				stepID: st.StepID, tool: st.Tool,
				path: outStr(st.Output, "src"), want: wantAbsent,
				exact: -1, atLeast: -1,
			})
			put(artifactExpectation{
				stepID: st.StepID, tool: st.Tool,
				path: outStr(st.Output, "dst"), want: wantExists,
				exact: -1, atLeast: -1,
			})
		case "file.delete":
			put(artifactExpectation{
				stepID: st.StepID, tool: st.Tool,
				path: outStr(st.Output, "path"), want: wantAbsent,
				exact: -1, atLeast: -1,
			})
		}
	}

	facts := make([]artifactFact, 0, len(order))
	for _, p := range order {
		facts = append(facts, checkArtifact(final[p]))
	}
	return facts
}

// checkArtifact 对一条事后条件做实测。
func checkArtifact(e artifactExpectation) artifactFact {
	f := artifactFact{stepID: e.stepID, tool: e.tool, path: e.path}
	fi, err := os.Stat(e.path)
	exists := err == nil

	switch e.want {
	case wantAbsent:
		if exists {
			f.detail = "步骤自述成功，但该路径仍然存在"
			return f
		}
		f.ok = true
		f.detail = "已不存在，与预期一致"
	case wantDir:
		switch {
		case !exists:
			f.detail = "步骤自述成功，但目录不存在"
		case !fi.IsDir():
			f.detail = "步骤自述成功，但该路径存在且不是目录"
		default:
			f.ok = true
			f.detail = "目录存在"
		}
	case wantExists:
		if !exists {
			f.detail = "步骤自述成功，但目标不存在"
			return f
		}
		f.ok = true
		f.detail = "存在"
	default: // wantFile
		switch {
		case !exists:
			f.detail = "步骤自述成功，但文件不存在"
		case fi.IsDir():
			f.detail = "步骤自述成功，但该路径是目录、不是文件"
		case e.exact >= 0 && fi.Size() != int64(e.exact):
			f.detail = fmt.Sprintf("文件在，但长度不符：实测 %d 字节，写入的内容应为 %d 字节", fi.Size(), e.exact)
		case e.atLeast >= 0 && fi.Size() < int64(e.atLeast):
			f.detail = fmt.Sprintf("文件在，但内容不足：实测 %d 字节，仅追加的内容就有 %d 字节", fi.Size(), e.atLeast)
		default:
			// 长度对得上，但可能写进去的是一份坏掉的 JSON——只看长度发现不了。
			if e.jsonCheck {
				if broken, why := jsonBroken(e.path); broken {
					f.detail = why
					return f
				}
			}
			f.ok = true
			f.detail = fmt.Sprintf("文件存在，%d 字节", fi.Size())
		}
	}
	return f
}

// failedArtifacts 取出核对未通过的条目。
func failedArtifacts(facts []artifactFact) []artifactFact {
	var bad []artifactFact
	for _, f := range facts {
		if !f.ok {
			bad = append(bad, f)
		}
	}
	return bad
}

// artifactsSatisfied 产物核对是否全部通过。没有可核对的产物时返回 true（不干预原有流程）。
func artifactsSatisfied(facts []artifactFact) bool {
	return len(failedArtifacts(facts)) == 0
}

// artifactFailureNote 把产物核对失败写成一句能照着改的话。
func artifactFailureNote(bad []artifactFact) string {
	parts := make([]string, 0, len(bad))
	for _, f := range bad {
		parts = append(parts, fmt.Sprintf("%s（%s 步骤 %s）：%s", f.path, f.tool, f.stepID, f.detail))
	}
	return fmt.Sprintf("产物核对未通过 %d 项——%s", len(bad), strings.Join(parts, "；"))
}

// artifactDigest 把实测事实写进反思器看到的材料。
//
// 只有存在**未通过**的条目时才加一句提醒，全部通过时不加：全部通过是常态，
// 每轮都叮嘱一遍会稀释真正需要注意的东西。
func artifactDigest(facts []artifactFact) string {
	if len(facts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 产物核对（代码实测，不是步骤自述）\n")
	for _, f := range facts {
		mark := "[通过]"
		if !f.ok {
			mark = "[不通过]"
		}
		fmt.Fprintf(&b, "- %s %s（%s）→ %s\n", mark, f.path, f.tool, f.detail)
	}
	if bad := failedArtifacts(facts); len(bad) > 0 {
		fmt.Fprintf(&b, "\n以上 %d 项未通过。注意「步骤自述成功」不等于「产物真的在」——判定验收标准时以本段实测为准。\n", len(bad))
	}
	return b.String()
}

// outStr 从工具返回的 payload 里取一个字符串字段（payload 通常是 map[string]any）。
func outStr(v any, key string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// argBool 从步骤的实际参数里取一个布尔字段，缺省为 false。
func argBool(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	b, _ := args[key].(bool)
	return b
}

// looksJSON 这个路径是不是"应该是一份 JSON"。只认扩展名，不猜内容。
//
// 刻意只覆盖 json / jsonl / ndjson：csv 那种"列数不齐也可能是合法数据"的格式不查，
// 宁可不判，不可错判。
func looksJSON(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, ".ndjson")
}

// jsonBroken 判定一份 JSON 文件是否写坏了，返回 (是否写坏, 说明)。
//
// 放宽一档：整体解析不通、但**每一非空行**都是合法 JSON 时也算通过——
// JSONL 常被写成 .json，为它报"格式非法"就是假警报，而假警报会让这道闸被当成噪音关掉。
//
// 空内容与纯空白内容不需要单独判：它们在这里本来就会走到最后一行
// （整体解析失败，但每一"行"都是空白、被跳过），同样返回"没写坏"。
// 曾经写过一句 `if len(bytes.TrimSpace(b)) == 0 { return false, "" }` 的显式守卫，
// 变异验证时发现**去掉它测试照样全绿**——它不是守卫，只是一句冗余分支，已删。
//
// 文件读不出来也不判：存在性/权限问题由前面几档负责，这里再报一次只是重复记账。
func jsonBroken(path string) (bool, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}) // 顺手容忍 UTF-8 BOM
	var v any
	if json.Unmarshal(b, &v) == nil {
		return false, ""
	}
	for _, ln := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		if json.Unmarshal(ln, &v) != nil {
			return true, "文件在、长度也对，但内容不是合法 JSON（格式不合法）"
		}
	}
	return false, ""
}
