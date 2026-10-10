// Package prompt 把「问模型一次」做成一个普通工具——也就是「提示词步骤」。
//
// **为什么是一个工具，而不是给技能加一种新步骤**：Gleam 的技能就是"每步调一个工具"
// 的步骤表（`types.Step.Tool`）。要让外部技能（Claude 的 SKILL.md 那类**提示词包**）
// 真能跑，缺的不是新的步骤语义，而是"一个能把提示词交给模型的工具"。做成工具之后：
//   - 技能格式、执行器、门控、留痕全都不用动；
//   - 它在计划里和别的步骤一样能被批准、重试、复盘、看用量。
//
// **权限为什么是只读放行**：这一步不写盘、也不发往新地方——它发的正是模型服务，
// 而那条边界本来就在任务全程被使用（台账里由 `kind=llm` 那一行交代）。
// 只拦中间这一步既拦不住什么，又让技能没法用。
package prompt

import (
	"context"
	"fmt"
	"strings"

	"gleam/internal/llm"
	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// ClientProvider 每次执行时现取模型客户端。用 provider 而不是直接持有：
// 设置页换模型/厂商之后，下一次调用就该用新的那一个（与 git 工具同一个理由）。
type ClientProvider func() llm.Client

// GenerateTool 把一段提示词交给模型并返回它的回答。
type GenerateTool struct {
	provider ClientProvider
}

func NewGenerate(p ClientProvider) *GenerateTool { return &GenerateTool{provider: p} }

func (t *GenerateTool) Name() string { return "prompt.run" }

func (t *GenerateTool) Description() string {
	return "把一段提示词交给模型执行，返回它的回答。外部技能（提示词包）靠它落地：技能正文就是提示词，这一步负责跑。"
}

func (t *GenerateTool) Permission() types.Permission { return types.PermissionReadOnly }

func (t *GenerateTool) Schema() map[string]any {
	return toolutil.Schema("调用模型执行一段提示词", []string{"prompt"}, map[string]any{
		"prompt": toolutil.SchemaProp("要执行的提示词（技能正文照原样放这里）", "string"),
		"system": toolutil.SchemaProp("可选的系统提示词", "string"),
		"input":  toolutil.SchemaProp("可选：本次要处理的素材，会附在提示词之后", "string"),
	})
}

func (t *GenerateTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	raw, err := toolutil.RequireStr(args, "prompt")
	if err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(raw)
	if prompt == "" {
		return nil, fmt.Errorf("提示词为空")
	}
	if t.provider == nil {
		return nil, fmt.Errorf("提示词步骤没接上模型客户端")
	}
	client := t.provider()
	if client == nil {
		return nil, fmt.Errorf("当前没有可用的模型：请先在「设置 → 模型」里接入一个")
	}
	if input := strings.TrimSpace(toolutil.Str(args, "input")); input != "" {
		prompt = prompt + "\n\n---\n\n" + input
	}
	text, err := client.Chat(ctx, llm.ChatRequest{
		System: strings.TrimSpace(toolutil.Str(args, "system")),
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型失败：%w", err)
	}
	return map[string]any{"text": text, "model": client.Name()}, nil
}
