// Package agent 实现 Gleam 的自主任务引擎：Plan-Execute-Reflect 三阶段循环。
package agent

import (
	"gleam/pkg/types"
)

// Notifier 事件出口，由宿主实现（CLI 控制台 / JSON-RPC 服务）。
type Notifier interface {
	// OnProgress 进度推送（流式规划文本也经此通道）。
	OnProgress(ev types.ProgressEvent)
	// OnApproval 阻塞请求用户批准；宿主负责展示计划并等待答复。
	OnApproval(req types.ApprovalRequest) types.ApprovalResponse
	// OnSuggestion 主动提议（人格化协作层）。
	OnSuggestion(taskID, text string)
	// OnSuggestSkill 技能固化建议。
	OnSuggestSkill(taskID string, draft types.SkillDraft)
	// OnTaskDone 一次**后台**任务（定时 / 变化触发）跑完。
	//
	// 为什么与 OnProgress 分开：进度是"正在发生"，宿主丢了就丢了——没人在看是正常的；
	// 完成是"已经发生且必须被记住"，尤其是失败。定时任务的整个价值就是"不用盯着"，
	// 结果不送达，用户恰恰必须盯着——不盯就不知道它坏了多久。
	// 是否打扰由调用方按任务的通知策略先判（见 scheduler.Job.ShouldNotify）。
	OnTaskDone(ev types.TaskDoneEvent)
}

// NopNotifier 空实现（测试用）。
type NopNotifier struct{}

func (NopNotifier) OnProgress(types.ProgressEvent) {}
func (NopNotifier) OnApproval(types.ApprovalRequest) types.ApprovalResponse {
	return types.ApprovalResponse{}
}
func (NopNotifier) OnSuggestion(string, string)             {}
func (NopNotifier) OnSuggestSkill(string, types.SkillDraft) {}
func (NopNotifier) OnTaskDone(types.TaskDoneEvent)          {}

// fanout 将事件分发给多个 Notifier。
type fanout []Notifier

func (f fanout) OnProgress(ev types.ProgressEvent) {
	for _, n := range f {
		n.OnProgress(ev)
	}
}
func (f fanout) OnApproval(req types.ApprovalRequest) types.ApprovalResponse {
	// 审批只取第一个有意见的实现
	for _, n := range f {
		return n.OnApproval(req)
	}
	return types.ApprovalResponse{}
}
func (f fanout) OnSuggestion(taskID, text string) {
	for _, n := range f {
		n.OnSuggestion(taskID, text)
	}
}
func (f fanout) OnSuggestSkill(taskID string, draft types.SkillDraft) {
	for _, n := range f {
		n.OnSuggestSkill(taskID, draft)
	}
}
func (f fanout) OnTaskDone(ev types.TaskDoneEvent) {
	for _, n := range f {
		n.OnTaskDone(ev)
	}
}
