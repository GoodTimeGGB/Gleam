package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 四类状态的落盘清单（Q4：配置 / 会话 / 记忆 / 任务）。
//
// **为什么要这一段**：资料把云上多租户的"按用户管理"拆成这四类。单机版的对应问题是
// 同一个：这四类各自的**落盘位置、生命周期、谁能改**，能不能一口答上来？
// 此前这些散在 README 的架构图（那是**代码**布局，不是数据布局）、设计文档十几个小节
// 和若干个 `filepath.Join` 里——用户问"我的记忆存在哪""删了会不会回来"时，
// 没有一个地方能一眼答完，而 agent 会一遍遍重新 grep 源码。
//
// **这一段是报告，不是判定**，所以刻意不做成 readiness 的第 10 项：
//
//	① 九项与资料的九个坑**一一对应**（readiness.go 的注释与 readiness_test.go 的
//	   顺序断言都钉着这一点），塞进第十项就把那份对应关系糊掉了；
//	② 数据目录里大部分东西是"用起来才有"——没用过记忆就没有 memory/，没跑过定时任务
//	   就没有 schedules.json。把它当判定会产生一批**假不合格**，然后用户加个开关把整个
//	   `--strict` 废掉——这正是 strictErr 注释里防的那件事。
//
// 所以：只报位置与体量，缺目录照常打印 `[无]`，不影响退出码。
//
// **路径清单只写一份**：就在下面的 stateEntries。设计文档 §4.6.26 写四类的语义与理由、
// 不重复列路径（重复列就会漂移），需要看路径就跑 `gleam doctor`。
//
// **`--json` 不带这一段**：它是给人看的地图，不是判定；CI 要的是结论。
func stateReport(dataDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n状态落盘（四类：配置 / 会话 / 记忆 / 任务）  数据目录 %s\n", dataDir)
	fmt.Fprintf(&b, "  只报位置与体量，不校验内容——缺目录是常态（没用过就没有），不是不合格\n")
	fmt.Fprintf(&b, "  数据目录之外的条目按**当前目录**解析（配置的主文件就是这么找的），换目录跑会显示 [无]\n")

	for _, c := range stateCategories {
		fmt.Fprintf(&b, "\n  %s · %s\n", c.Name, c.Meaning)
		fmt.Fprintf(&b, "      谁能改：%s\n", c.Who)
		for _, e := range stateEntries {
			if e.Category != c.Name {
				continue
			}
			fmt.Fprintf(&b, "      %-28s %s · %s\n", e.Path, describeStatePath(dataDir, e), e.Role)
		}
	}
	return b.String()
}

// stateCategory 一类状态的语义。顺序即打印顺序（四类在前，资产在后）。
type stateCategory struct {
	Name    string
	Meaning string
	Who     string
}

// stateCategories 四类 + 一类的语义。
//
// 第五类「资产」不是凑数：资料的框架里**没有它的位置**——云上多租户里"能力"由平台提供，
// 用户不持有技能文件。Gleam 的技能与片段是用户自己攒下来的东西，既不是配置（不决定怎么跑）、
// 也不是记忆（不是"发生过什么"）。硬塞进四类里，读者会以为它们和长期记忆同寿、同一种删法。
// 所以单列，并在设计文档里说清它为什么在四类之外。
var stateCategories = []stateCategory{
	{
		Name:    "配置",
		Meaning: `决定"怎么跑"：模型、档位、安全姿态、工作区`,
		Who:     "Web UI 设置页 / GLEAM_* 环境变量 / 手改 yaml",
	},
	{
		Name:    "会话",
		Meaning: "说过什么：一轮轮对话与它们的滚动上下文",
		Who:     "对话本身（自动落盘）/ Web UI 会话列表",
	},
	{
		Name:    "记忆",
		Meaning: "跨会话攒下来的：长期记忆与成长日志",
		Who:     "memory.save / memory.delete（经审批）/ Web UI 记忆页",
	},
	{
		Name:    "任务",
		Meaning: "每一次执行留下的痕迹",
		Who:     "引擎（自动落盘）/ gleam pending --clear",
	},
	{
		Name:    "资产",
		Meaning: "固化下来的能力与片段（四类之外，见设计文档 §4.6.26）",
		Who:     "技能市场 / skills/save / 片段工具",
	},
}

// stateEntry 一条落盘位置。
//
// Path 用正斜杠写，跨平台拼路径时统一过 filepath.FromSlash。
type stateEntry struct {
	Category string
	Path     string
	Role     string
	Outside  bool // 数据目录之外（配置的主文件：它由 --config 指定，不在数据目录里）
}

// stateEntries 全量落盘清单——**这是路径的唯一 owner**（见文件头）。
//
// 加一处落盘就加一行；忘了加也不会静默：state_test.go 有一条测试真跑一遍 runtime，
// 把盘上真实出现的顶层条目与这张表对照。
var stateEntries = []stateEntry{
	// ── 配置 ──
	{"配置", "settings.yaml", "设置覆盖层（UI 保存即热生效；删掉就回到主配置）", false},
	{"配置", "credentials.json", "模型凭证（与覆盖层分开存，免得随设置一起被导出）", false},
	{"配置", "configs/config.yaml", "主配置（默认找当前目录下的这个文件；--config 可指定）", true},

	// ── 会话 ──
	{"会话", "conversations", "每个会话一个 json（消息、时间、用量）", false},
	{"会话", "spaces", "微光空间：按工作文件夹分组会话；state.json 记当前空间", false},
	{"会话", "memory/context.json", "滚动上下文与压缩状态（住在 memory/ 下，但它是会话状态）", false},

	// ── 记忆 ──
	{"记忆", "memory/longterm.json", "长期记忆（容量淘汰 + 相似度判重 + 软删）", false},
	{"记忆", "growth.json", "成长日志：任务统计与等级（只增，用于跨会话迭代）", false},

	// ── 任务 ──
	{"任务", "tasks", "任务终态记录（回放 / 归因 / 质量统计的分母来源）", false},
	{"任务", "runs", "运行中日志：每步追加一行，正常结束即删（所以空是常态）", false},
	{"任务", "replays", "回放与分叉产物（刻意不进 tasks/，否则污染统计分母）", false},
	{"任务", "schedules.json", "定时任务：Cron / 间隔 / 触发条件 + 通知策略", false},
	{"任务", "pending_approvals.json", "等待审批的任务（进程退出后重启仍能列出）", false},
	{"任务", "audit.jsonl", "全量审计：追加式，含自动放行与数据出网留痕", false},
	{"任务", "tool-output", "超长工具输出的落盘缓存（按任务数裁剪，不是归档）", false},
	{"任务", "snapshots", "写前快照：改动清单能还原的依据（按任务数裁剪，含原文，权限 0600）", false},
	{"任务", "cues.json", "候补目标的处置记录（别再提 / 已采纳，含目标原文，权限 0600）；卡片本身不落盘，由任务历史现算", false},
	{"任务", "geo_history.json", "GEO 生成记录（生成式引擎优化板块的历史）", false},

	// ── 资产 ──
	{"资产", "skills", "固化的工作流（YAML，同名再存版本号自增）", false},
	{"资产", "snippets.json", "文本片段（支持变量展开）", false},
}

// describeStatePath 描述一条落盘位置的现状。
//
// 目录报「N 个文件 · 体量」，文件报体量，不在就报 `[无]`——**不报错**：
// 缺是常态（见文件头第 ② 条），把它变成错误等于让每个调用点都得先绕开一次预期中的失败。
func describeStatePath(dataDir string, e stateEntry) string {
	root := dataDir
	if e.Outside {
		root = "."
	}
	full := filepath.Join(root, filepath.FromSlash(e.Path))
	info, err := os.Stat(full)
	if err != nil {
		return "[无]"
	}
	if !info.IsDir() {
		return humanBytes(info.Size())
	}
	n, bytes, err := countTree(full)
	if err != nil {
		return "[读不到]"
	}
	return fmt.Sprintf("%d 个文件 · %s", n, humanBytes(bytes))
}

// countTree 递归数一个目录下的文件数与总字节数。
//
// 递归而不是只看一层：`tool-output/<taskID>/<stepID>.txt` 与 `skills/` 下的层级不一样，
// 只看一层会让前者永远显示"0 个文件"——而它恰恰是最需要知道体量的那个（它是缓存，
// 裁剪失效时第一个涨起来的就是它）。
func countTree(dir string) (int, int64, error) {
	var n int
	var bytes int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			// 单个条目读不到（权限、正被写）不该让整段报告失败：报的是"盘上有什么"，
			// 少一个条目的体量不影响结论，而整段消失会让人以为这段功能坏了。
			return nil
		}
		if d.IsDir() {
			return nil
		}
		n++
		if info, ierr := d.Info(); ierr == nil {
			bytes += info.Size()
		}
		return nil
	})
	return n, bytes, err
}

// humanBytes 人类可读的体量。1024 进制、一位小数——这一段是给人扫一眼的，
// 精确到字节没有意义（要精确就去看文件本身）。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KB", "MB", "GB"}
	for i, u := range units {
		value /= unit
		if value < unit || i == len(units)-1 {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%d B", n)
}
