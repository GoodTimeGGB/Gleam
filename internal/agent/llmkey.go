// llmkey.go — 「这把 key 允许发给谁」的单一出口。
//
// 背景（2026-09-24 PM 走查）：密钥此前只按"有没有"管理。在设置页把厂商从 A 换成 B，
// cfg.LLM.APIKey 里那把 A 的密钥会被原样带到 B 的端点上——本地优先的产品替用户
// 把凭证转发给了第三方，而这在界面上完全看不出来。
//
// 所以密钥必须和它要发往的主机一起存、一起判。规则只写在这一个文件里：
// 启动装配（cmd/gleam）、设置保存、连接自测、拉模型列表都问这里，
// 少问一处就是"判据对、线没接"。
package agent

import (
	"fmt"
	"strings"

	"gleam/internal/config"
	"gleam/internal/harness/credentials"
	"gleam/internal/llm"
)

// LLMKeyFor 只读地回答："发往 baseURL 时该用哪把密钥"。
// 第二个返回值是本地存着的那把 key 所属的主机（没存过则空）——界面要靠它区分
// "没配密钥"与"配过，但配的是别家"，否则用户只会看到一个假的"未设置"。
func LLMKeyFor(cfg *config.Config, creds *credentials.Store, baseURL, explicit string) (key, storedHost string) {
	explicit = strings.TrimSpace(explicit)
	scope := llm.KeyScope(baseURL)
	switch {
	case explicit != "":
		return explicit, scope
	case cfg != nil && cfg.LLM.APIKey != "" && cfg.LLM.APIKeyScope == scope:
		// 内存里已生效的那把，主机对得上才用（环境变量/config.yaml 注入的也走这条，
		// 它们在启动时被 MarkInjectedLLMKey 标上了当次主机）。
		return cfg.LLM.APIKey, scope
	}
	if creds == nil {
		return "", ""
	}
	return creds.ResolveLLMAPIKey(scope)
}

// BindLLMKey 确定"发往 baseURL 用哪把密钥"并写回 cfg（生效值 + 主机标记）。
// explicit 非空 = 用户刚在表单里填了新 key，落盘并绑到这台主机；
// clear = 用户点了清除，凭证与内存一起清掉；
// 两者都没有 = 按主机取用，取不到就把内存里的 key 清空——**绝不沿用上一家的**。
// 返回实际生效的密钥。
func BindLLMKey(cfg *config.Config, creds *credentials.Store, baseURL, explicit string, clear bool) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("配置为空，无法绑定密钥")
	}
	scope := llm.KeyScope(baseURL)
	explicit = strings.TrimSpace(explicit)
	if explicit != "" && scope == "" {
		// 没有主机就存不下来。此时若只放进内存，下次启动它就变成"没绑主机的老密钥"，
		// 补绑逻辑会把它随手发给第一个连的主机——那正是这里要挡的事，所以直接拒绝。
		return "", fmt.Errorf("请先填写 base_url 再保存密钥：密钥按接入主机绑定")
	}
	if clear {
		if creds != nil {
			if err := creds.SetLLMAPIKey("", ""); err != nil {
				return "", fmt.Errorf("密钥已清除但本地凭证文件更新失败: %w", err)
			}
		}
		cfg.LLM.APIKey, cfg.LLM.APIKeyScope = "", ""
		return "", nil
	}
	if explicit != "" {
		if creds != nil {
			if err := creds.SetLLMAPIKey(explicit, scope); err != nil {
				return "", fmt.Errorf("密钥已生效但保存到本地失败: %w", err)
			}
		}
		cfg.LLM.APIKey, cfg.LLM.APIKeyScope = explicit, scope
		return explicit, nil
	}
	key, _ := LLMKeyFor(cfg, creds, baseURL, "")
	cfg.LLM.APIKey, cfg.LLM.APIKeyScope = key, scope
	if key == "" {
		cfg.LLM.APIKeyScope = ""
	}
	return key, nil
}

// MarkInjectedLLMKey 给"随配置进来的"密钥标上它此刻要发往的主机。
// 必须在 ResolvePreset 之后调用：那时 base_url 才是真正生效的那一个。
// 只做一次（已标过的不再改），因为标记的含义是"这把 key 当初是配给谁的"。
func MarkInjectedLLMKey(cfg *config.Config, baseURL string) {
	if cfg == nil || cfg.LLM.APIKey == "" || cfg.LLM.APIKeyScope != "" {
		return
	}
	cfg.LLM.APIKeyScope = llm.KeyScope(baseURL)
}

// llmKeyFor 门面内的只读版本（连接自测、拉模型列表、设置视图都走它）。
func (a *Agent) llmKeyFor(baseURL, explicit string) (key, storedHost string) {
	if a == nil {
		return "", ""
	}
	return LLMKeyFor(a.Cfg, a.Creds, baseURL, explicit)
}
