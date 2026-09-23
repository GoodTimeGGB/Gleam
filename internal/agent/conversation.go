package agent

import (
	"strings"

	"gleam/internal/harness/conversation"
	"gleam/pkg/types"
)

// appendConvo 向指定会话追加一条消息；存储未装配或会话 ID 为空时静默跳过。
// 存储错误不阻断主流程（会话记录是旁路能力）。
func (a *Agent) appendConvo(convoID string, msg conversation.Message) {
	if a.Convos == nil || strings.TrimSpace(convoID) == "" {
		return
	}
	if strings.TrimSpace(msg.Content) == "" {
		return
	}
	_, _ = a.Convos.Append(convoID, msg)
}

// convoReply 从执行结果提取用于会话记录的助手回复文本。
func convoReply(r *types.GoalResult) string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.Summary) != "" {
		return r.Summary
	}
	if strings.TrimSpace(r.Error) != "" {
		return "⚠️ " + r.Error
	}
	if strings.TrimSpace(r.Suggestion) != "" {
		return r.Suggestion
	}
	return ""
}

// LoadConversationContext 切换到历史会话时，把该会话的历史轮次载入短期记忆，
// 使用户可在旧上下文上继续对话。同时若会话所属空间绑定了文件夹，联动切换工作区。
// 返回会话详情；存储未装配时返回错误。
func (a *Agent) LoadConversationContext(convoID string) (*conversation.Conversation, error) {
	c, err := a.Convos.Get(convoID)
	if err != nil {
		return nil, err
	}
	if a.Mem != nil {
		a.Mem.ResetConversation()
		for _, m := range c.Messages {
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			a.Mem.AddTurn(m.Role, m.Content)
		}
	}
	// 联动：打开某空间的对话时，激活该空间；若其绑定了文件夹则同步切换工作区
	if a.Spaces != nil {
		sid := c.SpaceID
		if sid == "" {
			sid = conversation.DefaultSpaceID
		}
		_ = a.Spaces.SetActive(sid)
		if sp, err := a.Spaces.Get(sid); err == nil && strings.TrimSpace(sp.Path) != "" {
			_ = a.applyWorkspace(sp.Path)
		}
	}
	return c, nil
}

// NewConversation 创建一个属于指定空间（空则当前激活空间）的空会话并重置短期上下文。
func (a *Agent) NewConversation(spaceIDs ...string) (*conversation.Conversation, error) {
	spaceID := ""
	if len(spaceIDs) > 0 {
		spaceID = strings.TrimSpace(spaceIDs[0])
	}
	if spaceID == "" {
		spaceID = a.ActiveSpaceID()
	}
	c, err := a.Convos.Create(spaceID)
	if err != nil {
		return nil, err
	}
	if a.Mem != nil {
		a.Mem.ResetConversation()
	}
	return c, nil
}

// ActiveSpaceID 返回当前激活空间 ID（未装配或读取失败时回落默认空间）。
func (a *Agent) ActiveSpaceID() string {
	if a.Spaces == nil {
		return conversation.DefaultSpaceID
	}
	if id, err := a.Spaces.ActiveID(); err == nil {
		return id
	}
	return conversation.DefaultSpaceID
}
