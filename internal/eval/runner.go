package eval

import (
	"context"
	"time"
	"unicode/utf8"

	"gleam/internal/agent"
	"gleam/pkg/types"
)

// DefaultTimeout 单条用例的默认超时。
const DefaultTimeout = 60 * time.Second

// Runner 按指定深度跑一组用例。
type Runner struct {
	Agent *agent.Agent
	Depth Depth
	// CWD 用例自己没写 CWD 时用它。full 深度会真的执行工具，
	// 所以调用方（CLI）应当把它指向一个临时目录，别让它动到用户的工作区。
	CWD string
	// Timeout 单条用例超时；<=0 用 DefaultTimeout。
	Timeout time.Duration
	// Tier 可选：**模拟**生效模型档位，只影响提示词构建，不切换模型客户端。
	//
	// 存在的理由：按档位条件化的规则（见 agent/rules.go）在真实环境里要"用户为这一档
	// 配了模型"才会生效，评测默认跑不到那条路径。给一个显式开关，才能用同一个评测框架
	// 量出"这一档的提示词长了/短了多少"，而不是只在单测里相信它。
	// 它不改变工具筛选结果，所以对通过率没有影响——只对 prompt_chars 有影响。
	Tier string
	// OnCase 可选：每跑完一条回调一次，供 CLI 边跑边输出。
	OnCase func(CaseResult)
	// Repeat 每条用例重跑的遍数（<=1 表示只跑一遍，保持原有行为与基线可比）。
	//
	// 存在的理由：通过率只回答"对不对"，回答不了"稳不稳"。上一轮减法实验里，
	// 同一配置重跑 16 条用例有 3.00/5.67 条工具集不同，而处理效应只有 4.83——
	// 噪声比效应还大，结论不可归因。要能事先看出这件事，就得让评测自己重复采样。
	//
	// 注意口径：报告里的通过率仍然取**首次运行**的结果，这样 --repeat 1 与 --repeat 3
	// 的通过率是可比的；重跑结果只用来算可重复性，不会反过来改写通过率。
	Repeat int
}

// plannerFor 装配规划器，并按需覆盖生效档位。
func (r *Runner) plannerFor(req types.GoalRequest) *agent.Planner {
	p := r.Agent.BuildPlanner(req)
	if r.Tier != "" {
		p.Tier = r.Tier
	}
	return p
}

// Run 跑完所有用例并汇总。
func (r *Runner) Run(ctx context.Context, cases []Case) Report {
	rep := Report{
		GeneratedAt: time.Now(),
		Depth:       r.Depth,
		Model:       r.modelName(),
		// select 深度把 Helper 置空、只走本地关键词打分，所以这里如实标注。
		LocalSelect:  r.Depth == DepthSelect,
		RelevantOnly: r.Depth == DepthSelect,
		Tier:         r.Tier,
		RuleSet:      agent.RuleSetVersion(),
		Total:        len(cases),
	}
	for _, c := range cases {
		res := r.runCase(ctx, c)
		tally(&rep, c, &res)
		rep.PromptChars += res.PromptChars
		rep.RetryOK += res.RetryOK
		for k, n := range res.FailureKinds {
			if rep.FailureBreakdown == nil {
				rep.FailureBreakdown = map[string]int{}
			}
			rep.FailureBreakdown[k] += n
		}
		rep.Cases = append(rep.Cases, res)
		if r.OnCase != nil {
			r.OnCase(res)
		}
	}
	// 可重复性：与通过率**并列但口径不同**——通过率是"首次运行对不对"，
	// 可重复性是"重跑稳不稳"。两个数不能互相替代，所以分开算、分开放。
	if r.Repeat > 1 && len(rep.Cases) > 0 {
		rep.RepeatN = r.Repeat
		for _, cr := range rep.Cases {
			if cr.Stable {
				rep.StableCases++
				continue
			}
			rep.UnstableCases = append(rep.UnstableCases, cr.ID)
		}
		rate := float64(rep.StableCases) / float64(len(rep.Cases))
		rep.Repeatability = &rate
	}
	return rep
}

// tally 把一条结果计入报告。
//
// 抽出来是为了能单测："已知问题"这一档的归集规则（红的算已知、绿的算已修复）
// 是门禁是否可信的关键，不该埋在循环里没法单独验。
// 不可解用例（full 深度）走第三条轴：识别成功计入 Handled（不计入 Passed），
// 识别失败计入 Failed——把它混进通过率，等于激励模型对做不到的事硬猜"做完了"。
// Attempted（通过率分母 = Total - Unsolvable）在这里一并维护。
func tally(rep *Report, c Case, res *CaseResult) {
	if LayerOf(c) == LayerHoldout {
		rep.HoldoutTotal++
		if res.Passed {
			rep.HoldoutPassed++
		}
	}
	if c.Unsolvable && rep.Depth == DepthFull {
		rep.Unsolvable++
		if res.Passed {
			rep.UnsolvableHandled++
		} else {
			rep.Failed++
		}
		rep.Attempted = rep.Total - rep.Unsolvable
		return
	}
	switch {
	case res.Passed:
		rep.Passed++
		if c.KnownIssue {
			// 标着"已知问题"却过了 —— 根因多半已修，该摘标记了。
			rep.Resolved = append(rep.Resolved, c.ID)
		}
	case c.KnownIssue:
		res.Known = true
		rep.Known++
	default:
		rep.Failed++
	}
	rep.Attempted = rep.Total - rep.Unsolvable
}

func (r *Runner) modelName() string {
	if r.Agent == nil || r.Agent.Cfg == nil {
		return "unknown"
	}
	if r.Agent.Cfg.LLM.Provider == "mock" {
		return "mock"
	}
	if m := r.Agent.Cfg.LLM.Model; m != "" {
		return m
	}
	if p := r.Agent.Cfg.LLM.Provider; p != "" {
		return p
	}
	return "unknown"
}

func (r *Runner) runCase(ctx context.Context, c Case) CaseResult {
	n := r.Repeat
	if n < 1 {
		n = 1
	}
	// 首遍就是"这条用例的结果"，多遍只用来回答"稳不稳"。
	res := r.runCaseOnce(ctx, c)
	if n == 1 {
		res.Runs, res.Agreed, res.Stable = 1, 1, true
		return res
	}
	sigs := []string{runSignature(res.Passed, res.Steps, res.Tools)}
	for i := 1; i < n; i++ {
		again := r.runCaseOnce(ctx, c)
		sigs = append(sigs, runSignature(again.Passed, again.Steps, again.Tools))
	}
	_, agree := modalVariant(sigs)
	res.Runs, res.Agreed, res.Stable = n, agree, agree == n
	if !res.Stable {
		res.Variants = describeVariants(sigs)
	}
	return res
}

// runCaseOnce 跑一遍用例：观测 → 判定。重复采样与单次执行共用这一条路径，
// 保证 --repeat N 观测到的东西与 --repeat 1 完全同源。
func (r *Runner) runCaseOnce(ctx context.Context, c Case) CaseResult {
	start := time.Now()
	res := CaseResult{ID: c.ID, Goal: c.Goal, Note: c.Note, Holdout: LayerOf(c) == LayerHoldout}

	cwd := c.CWD
	if cwd == "" {
		cwd = r.CWD
	}
	req := types.GoalRequest{
		Goal:     c.Goal,
		Role:     c.Role,
		TaskMode: types.TaskMode(c.TaskMode),
		Mode:     string(types.ModeAuto),
		Context:  map[string]any{"cwd": cwd},
	}

	to := r.Timeout
	if to <= 0 {
		to = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	var obs observation
	switch r.Depth {
	case DepthPlan:
		p := r.plannerFor(req)
		// 这条用例场景下实际生效的规则：解释了提示词长短差异，也是回归时的归因线索。
		res.Rules = p.ActiveRules()
		res.PromptChars = utf8.RuneCountInString(p.SystemPromptFor(c.Goal, cwd))
		plan, err := p.Plan(cctx, c.Goal, cwd, nil, nil, "", "")
		if err != nil {
			obs.Error = err.Error()
		} else {
			obs.Tools = planTools(plan)
			obs.Steps = len(plan.Steps)
			obs.Acceptance = len(plan.Acceptance)
		}
	case DepthFull:
		p := r.plannerFor(req)
		// 这条用例场景下实际生效的规则：解释了提示词长短差异，也是回归时的归因线索。
		res.Rules = p.ActiveRules()
		res.PromptChars = utf8.RuneCountInString(p.SystemPromptFor(c.Goal, cwd))
		g := r.Agent.RunGoal(cctx, req)
		obs.Tools = stepTools(g.Steps)
		obs.Steps = len(g.Steps)
		obs.Acceptance = len(g.Acceptance)
		obs.Status = string(g.Status)
		obs.Error = g.Error
		res.Status = string(g.Status)
		res.Score = g.Score
		// 过程观测：结果绿不代表过程干净——失败步骤的归因分布与
		// "重试后才成功"的步骤数，是把"一次就对"与"侥幸做成"分开的依据。
		for k, n := range g.FailureBreakdown {
			if res.FailureKinds == nil {
				res.FailureKinds = map[string]int{}
			}
			res.FailureKinds[string(k)] += n
		}
		for _, st := range g.Steps {
			if st.Retried && st.Status == types.StepSucceeded {
				res.RetryOK++
			}
		}
	default: // DepthSelect
		p := r.plannerFor(req)
		// 这条用例场景下实际生效的规则：解释了提示词长短差异，也是回归时的归因线索。
		res.Rules = p.ActiveRules()
		// 确定性：配了辅助模型时筛选会走模型（结果不确定、还花钱）。
		// 那属于 plan/full 深度要覆盖的事；select 深度只回答"本地关键词打分认不认它相关"。
		p.Helper = nil
		res.PromptChars = utf8.RuneCountInString(p.SystemPromptFor(c.Goal, cwd))
		// 用 RelevantTools 而不是 SelectTools：后者是补齐过的最终菜单，永远填满，
		// "在菜单里"证明不了相关（详见该方法的注释）。
		obs.Tools = p.RelevantTools(c.Goal)
	}

	res.Tools = obs.Tools
	res.Steps = obs.Steps
	res.Acceptance = obs.Acceptance
	res.Error = obs.Error
	res.Checks = judge(c, obs, r.Depth)
	res.Passed = allPassed(res.Checks)
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

// planTools 取计划里用到的工具（去重、保持顺序）——同一工具在多个步骤出现只报一次。
func planTools(p types.Plan) []string {
	names := make([]string, 0, len(p.Steps))
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Tool == "" || seen[s.Tool] {
			continue
		}
		seen[s.Tool] = true
		names = append(names, s.Tool)
	}
	return names
}

// stepTools 取执行结果里用到的工具（去重、保持顺序）。
func stepTools(steps []types.StepResult) []string {
	names := make([]string, 0, len(steps))
	seen := map[string]bool{}
	for _, s := range steps {
		if s.Tool == "" || seen[s.Tool] {
			continue
		}
		seen[s.Tool] = true
		names = append(names, s.Tool)
	}
	return names
}

func allPassed(checks []Check) bool {
	for _, c := range checks {
		if !c.Passed {
			return false
		}
	}
	return true
}
