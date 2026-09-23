package agent

import "fmt"

// Narrator 人格化旁白层（自研，对应设计文档 §4.3 一致的协作风格）：
// 按协作风格为各阶段生成有温度、语气一致的进度叙述——像同事说话，不像日志刷屏。
// 纯模板实现：零 LLM 调用、零延迟、零 token、完全确定。
// 步骤级消息（✅/❌ 前缀）不经过旁白：前端以其判定时间线状态，测试断言也依赖原文。
type Narrator struct {
	Style string // rigorous | gentle | efficient（空视为 efficient）
}

func (n Narrator) style() string {
	switch n.Style {
	case "gentle", "rigorous":
		return n.Style
	default:
		return "efficient"
	}
}

// PlanStart 规划开始。
func (n Narrator) PlanStart() string {
	switch n.style() {
	case "gentle":
		return "好，交给我。先想清楚怎么做……"
	case "rigorous":
		return "目标已明确，开始拆解约束与步骤。"
	default:
		return "收到，先拆解一下。"
	}
}

// PlanDone 计划完成。
func (n Narrator) PlanDone(steps int) string {
	switch n.style() {
	case "gentle":
		return fmt.Sprintf("想好了，一共 %d 步，我开始咯。", steps)
	case "rigorous":
		return fmt.Sprintf("已拆解为 %d 步，依赖与参数均已校验，开始执行。", steps)
	default:
		return fmt.Sprintf("%d 步，开干。", steps)
	}
}

// Compressed 上下文压缩完成。
func (n Narrator) Compressed() string {
	switch n.style() {
	case "gentle":
		return "顺手把早期对话整理成了摘要，不会忘记之前聊过的。"
	case "rigorous":
		return "早期上下文已压缩为滚动摘要，关键信息保留。"
	default:
		return "早期上下文已压缩，省 token 不丢上下文。"
	}
}

// ReflectStart 反思开始。
func (n Narrator) ReflectStart() string {
	switch n.style() {
	case "gentle":
		return "我来看看做得怎么样……"
	case "rigorous":
		return "正在逐项核对执行结果与目标差距。"
	default:
		return "检查一下成果。"
	}
}

// Replan 触发重规划。
func (n Narrator) Replan(score int) string {
	switch n.style() {
	case "gentle":
		return fmt.Sprintf("完成度 %d/100，还有差距，我再想办法补一补。", score)
	case "rigorous":
		return fmt.Sprintf("完成度 %d/100，未达标，进入重规划并携带失败反馈。", score)
	default:
		return fmt.Sprintf("完成度 %d/100，差一点，换个打法再来。", score)
	}
}
