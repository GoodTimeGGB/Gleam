package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gleam/internal/config"
	"gleam/internal/harness/safety"
)

// pendingFile 等待审批记录的落盘文件名（与运行时装配处一致）。
const pendingFile = "pending_approvals.json"

// auditFile 审计落盘文件名（JSONL 追加式）。
const auditFile = "audit.jsonl"

// cmdPending 列出/清理「等待审批中」的任务（P4-2）。
//
// 它存在的意义：进程若在等待审批期间退出（用户关窗口、升级重启），
// 那条任务就没人知道它停在哪、要批什么。落盘 + 这个命令让"卡在审批"可见，
// 人工确认后重新提交即可——**不做完整断点续跑**（桌面单机场景下收益未验证）。
func cmdPending(args []string) error {
	fs := flag.NewFlagSet("pending", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	dataDir := fs.String("data-dir", "", "数据目录")
	clear := fs.Bool("clear", false, "清理全部等待审批记录（确认已重新提交后使用）")
	audit := fs.Bool("audit", false, "改为列出审计留痕（最近 50 条，落盘的 audit.jsonl）")
	egressOnly := fs.Bool("egress", false, "只列出数据出网留痕（配合 --audit：提示词发给了哪个模型服务、web.fetch 抓了哪个站点）")
	flagArgs, positionals := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("pending 不接受位置参数 %q", positionals[0])
	}

	dir, err := resolveDataDir(*configPath, *dataDir)
	if err != nil {
		return err
	}

	if *audit {
		// 只看出网时要先把全部读回来再筛：先取最近 50 条再筛，
		// 出网记录稀疏时会被别的留痕整个盖掉，看起来像"什么都没出网"——
		// 一个查不出问题的审计比没有审计更危险。
		limit := 50
		if *egressOnly {
			limit = 0
		}
		entries, err := safety.LoadAuditLog(filepath.Join(dir, auditFile), limit)
		if err != nil {
			return err
		}
		if *egressOnly {
			kept := make([]safety.AuditEntry, 0, len(entries))
			for _, e := range entries {
				if e.Action == "egress" {
					kept = append(kept, e)
				}
			}
			entries = kept
			if len(entries) > 50 {
				entries = entries[:50]
			}
		}
		if len(entries) == 0 {
			if *egressOnly {
				fmt.Println("没有数据出网留痕（模型调用与 web.fetch 会自动留痕到 " + filepath.Join(dir, auditFile) + "）")
				return nil
			}
			fmt.Println("没有审计记录（需要审批的动作与被拦动作会自动留痕到 " + filepath.Join(dir, auditFile) + "）")
			return nil
		}
		fmt.Printf("审计留痕（最近 %d 条，新的在前）\n\n", len(entries))
		for _, e := range entries {
			fmt.Printf("  %s  %-14s %-6s %-8s %s\n",
				e.Time.Format("01-02 15:04:05"), e.Tool, e.Risk, e.Action, firstLineOr(e.Reason))
			if e.Detail != "" {
				fmt.Printf("             %s\n", firstLineOr(e.Detail))
			}
		}
		return nil
	}

	path := filepath.Join(dir, pendingFile)
	if *clear {
		if err := safety.ClearPendingFile(path); err != nil {
			return err
		}
		fmt.Println("已清理等待审批记录")
		return nil
	}

	items, err := safety.LoadPendingApprovals(path)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("没有等待审批的任务")
		return nil
	}
	fmt.Printf("等待审批的任务（%d 条）——进程若已退出，请人工确认后重新提交\n\n", len(items))
	for _, p := range items {
		ago := time.Since(p.At).Round(time.Second)
		if ago < 0 {
			ago = 0 // 时钟偏差/未来时间戳不该显示成负数
		}
		fmt.Printf("  任务 %s  步骤 %s\n", p.TaskID, p.StepID)
		fmt.Printf("    工具 %s（风险 %s）等待 %v\n", p.Tool, p.Risk, ago)
		if p.Reason != "" {
			fmt.Printf("    原因：%s\n", p.Reason)
		}
	}
	fmt.Printf("\n确认已重新提交后，可执行 gleam pending --clear 清理。\n")
	return nil
}

// resolveDataDir 解析数据目录：显式给了就用，否则从配置读（与 buildRuntime 同一优先级）。
func resolveDataDir(configPath, dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) != "" {
		return dataDir, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	return cfg.DataDir, nil
}

func firstLineOr(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if s == "" {
		return "-"
	}
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}
