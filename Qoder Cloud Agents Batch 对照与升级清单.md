# Qoder Cloud Agents Batch · 对照与升级清单

**来源**：《Qoder Cloud Agents：Batch，让 Agent 任务批量执行、可靠交付》（Qoder 公众号，2026-09-21）。
**性质**：产品介绍文，主体是**云上批量任务平台**——用户把任务写成 JSONL 交给平台，平台映射成 N 个独立 Session 并行执行，再统一收集结果。
**与上一份的关系**：与本仓库已有的 `Qoder Cloud Agents 对照与升级清单.md` 是**同源不同篇**（那篇讲「Model API → Agent as a service」的云端多租户形态）。两篇判据几乎不重叠，但**结论方向一致**：云上形态的大部分内容与 Gleam 的定位不兼容。

> **落地进度（2026-09-23 更新）**：本清单**已全部收口** —— P0 → 设计文档 §4.6.27.1；
> P1 → §4.6.27.2；P2 → §4.6.27.3；P3 → §4.6.27.4（只动 `docs/known-limits.md` 一行）。
> 三批共 **11 条测试 + 14 处变异全部会响**；全量测试 27 个包全过；评测基线 `--baseline --strict` 退出码 0。
> 落地时改掉原计划的地方、以及落地中发现的**新事实**，逐条记在各 P 条目末尾（标「落地时」）。
> 第六节的「明确不做」是**决定**，不是待办。

## 摘要

三句话：

1. **哪里领先**——资料 A12③ 说「把 Agent 评测变成发布前的常规步骤」，而 Gleam 的 `gleam eval` **已经比它深**：分层 + 保留池 + 可重复性 + 基线回归 + badcase 回流，且已进 `verify.sh` 当门禁（`scripts/verify.sh:139`）。这条**不是缺口，是资产**（见第三节）。
2. **最大缺口**——资料 A10 把批量的验收标准总结成「**可追踪、可恢复、可对账、可交付**」。Gleam 的「批」只有评测一处，而它有一个缺口能让前三个词同时失效：**步骤在 goroutine 里跑，全仓没有 recover**（`internal/agent/executor.go:365`），一次 panic 不是让一条用例变红，而是让**整份报告消失**——已通过的、已花的 token 一起没。这是 P0。
3. **该立刻修的**——只有 P0 一条（约十行 + 一个测试，一处改动覆盖四条执行路径）。P1/P2 是「让已有的东西可见/可读回」，P3 只动一行文档。

---

## 一、资料讲了什么（判据表）

### A. 什么工作适合批量（判据句）

| # | 判据 |
| :--- | :--- |
| A1 | 适合 Batch 的任务**同时**具备五特征：①数量足够大 ②结构大致相似（可复用相同或少量几种 Template / 工具 / 输出要求）③任务之间彼此独立（不依赖上一项的中间状态，也不共同修改强一致数据）④允许异步返回（业务关心「这一批能否完整交付」而非每项几秒内响应）⑤**确实需要 Agent**（含语义判断、资料检索、文件处理、工具调用或多步骤执行）。**反判据**：一条 SQL 或确定性脚本就能稳定解决的，直接用它们更简单、更经济 |
| A2 | 满足的特征越多价值越明显；**不适合**的是：强顺序依赖、共享可变状态、低延迟交互、执行中必须等待人工审批 |
| A3 | 心智模型 = **Map + Collect**：提交时把任务写进 JSONL（一行一项独立工作），完成后统一收集状态、响应、产物与用量 |
| A4 | 每行可指定 `custom_id` / `template_id` / `identity_id` / `body.input` / `body.resources[]`（`type=file` + `file_id` + `mount_path`） |

### B. 批量的价值在哪（全文最强的一条）

| # | 判据 |
| :--- | :--- |
| A5 | **「在客户端写一个循环」与 Batch 不是一回事**：循环解决的是「把请求发出去」；Batch 解决的是「让一批工作被管理和交付」——①什么时候执行 ②失败后如何处理 ③进度如何追踪 ④**结果如何与原始任务一一对应**，这四件事由平台统一承担 |
| A10 | 「这些机制不会让 Agent 的判断天然正确，但能够保证一次批量执行**可追踪、可恢复、可对账、可交付**。这才是把实验性调用变成生产任务的基础。」 |
| A14 | 批量任务最怕的不是慢，而是**失控**：坏数据混入执行、个别任务卡住整批、执行节点中断后状态丢失、跑完后结果无法与原始输入对上 |

### C. 生命周期与可靠性机制

| # | 判据 |
| :--- | :--- |
| A6 | 提交后先做**前置校验**（JSONL 格式、必填字段、`custom_id`、所需资源）；**单行数据有问题时被单独标记，不必因此放弃其他可执行任务** |
| A7 | 每项任务**独立 Session**；超时、失败和重试**不会污染其他任务**；短暂故障平台自动重试；执行节点异常可**根据已保存的状态继续处理** |
| A8 | 调度同时受**容量、顺序、闲时时段**约束，避免一批任务挤占在线业务资源 |
| A9 | 完成后成功结果写 `output.jsonl`、最终失败项写 `error.jsonl`，**每行携带原始 `custom_id`** → 业务系统可稳定对账；也可继续获取文件产物与**用量信息** |
| A11 | 默认等到每日 **22:00–次日 08:00（UTC+8）** 闲时激活；`ignore_idle_window=true` 可跳过，但**不等于无条件立即执行**（仍受提交顺序、用户级互斥、平台总容量、节点可用性约束） |
| A13 | 起步建议**从小批次开始**：先选几十条数据 + 一个 Template，跑通「准备资源 → 提交 JSONL → 查进度 → 下载 output/error → 验证后再扩大」五步 |

### D. 场景与数字

| # | 判据 / 数字 |
| :--- | :--- |
| A12 | 三类高价值场景：**①从抽样检查到全量覆盖**（关键词规则覆盖不了、需要完整上下文判断）；**②让材料审查拥有稳定产能**（Template 定义审查方法，把散落在个人经验中的检查方法**固化下来**，让同一套规则可重复执行）；**③把 Agent 评测变成发布前的常规步骤**（样例相互独立、执行含多轮推理与工具调用、结果要与预期对照） |
| — | 单批最多 **10,000** 项；案例规模每天 **50 万–60 万**条会话；Service Account 错峰时段 **01:00–07:00**；错峰优惠截至 **2026-09-21**（仅影响 Credits 计价，模型质量不变） |

---

## 二、对照结果总览

判定：✅ 已做对 ｜ ⚠ 部分成立 ｜ ❌ 缺口 ｜ ➖ 不适用（附理由见第六节）

| 判据 | Gleam 现状（取证） | 判定 |
| :--- | :--- | :---: |
| A1 五特征 | Gleam 的「批」只有评测一处。用例集：几十条、结构相似、彼此独立、无低延迟要求、plan/full 深度确实需要 Agent——**五条全中** | ✅ |
| A1 末句（确定性脚本能解决就别用 Agent） | `eval --depth select` 就是这条：本地关键词打分、零模型调用、确定性、可离线进 CI（`cmd/gleam/eval.go:24-32`） | ✅ |
| A2 反例四类 | Gleam 没有「强顺序依赖 / 共享可变状态 / 低延迟 / 等人工审批」的**批**场景（单条顺序工作用 `gleam goal` 即可） | ➖ |
| A3 Map + Collect | `eval.Runner.Run` 逐条跑、逐条收（`internal/eval/runner.go:66-81`） | ✅ |
| A4 每行字段 | `eval.Case` / `CaseResult` 有等价物；`custom_id` → `Case.ID`（**人写的、稳定的**，不需要平台生成）；`identity_id` 单机不存在；`template_id` 由用例直接写 `goal`/`role` | ✅ |
| A5④ 对应关系 | `CaseResult.ID`（`internal/eval/eval.go:159`），报告逐条可对回用例 | ✅ |
| A5③ 进度 | `OnCase` 逐条回调打到 stderr（`cmd/gleam/eval.go:128-143`）+ `--json` 全量报告（`:170-175`） | ✅ |
| A5①② 执行时机 / 失败处理 | 时机：立即执行，单机无需调度；**失败处理：❌ 见 P0** | ⚠ |
| A6 前置校验 | `loadEvalCases` → `Validate`（`internal/eval/cases.go:67-92`）发生在**任何模型调用之前**（`cmd/gleam/eval.go:92` 早于 `:97` 的 `buildRuntime`）——用例集写坏时一分钱不花就报出来 | ✅ |
| A6 行级隔离 | `Validate` 遇第一条即 return（`cases.go:70-89`）→ 整批拒绝 | ➖ **刻意不适用**，理由见第六节 |
| A7 失败不污染其他任务 | ❌ 步骤在 goroutine 里跑（`internal/agent/executor.go:365`），**全仓无 recover**（`grep -rn "recover()"` 仅命中 `cmd/gleam/console_windows.go:17`）→ 一次 panic 整进程死，带走整批 | ❌ **P0** |
| A7 自动重试 | 单步重试已有（`executor.go:690-765`）；环境噪声的重试判据在 `scripts/lib/gonoise.sh` | ✅ |
| A7 按已保存状态继续 | 有 `runs/<id>.jsonl` 逐步 append（`internal/agent/runlog.go:44`），但**只保留没跑完的**（`DiscardRunLog`，`runlog.go:137`）；`replay --from` 是**手动**沿用（`cmd/gleam/replay.go:112-137`）；自动续跑**明确不做**（`cmd/gleam/pending.go:20-24`） | ⚠ |
| A8 容量 / 顺序 / 闲时 | ❌ 调度器无任务级容量约束（`internal/harness/scheduler/scheduler.go:210` 直接 `go fire(j)`）；`max_concurrency` 是**单任务**的（`executor.go:337` 每次计划执行各建一个信号量）→ N 个任务并发时实际是 N×8 | ⚠ **P3** |
| A9 `output.jsonl` / `error.jsonl` 分文件 | 无分文件；但有 `--json` 全量机器可读报告（含每条 `passed` 与逐条 `checks`），失败项 `jq` 一条筛出 | ➖ 不做，理由见第六节 |
| A9 对账键 | ✅ `CaseResult.ID` | ✅ |
| A9 用量信息 | 任务级有（`types.GoalResult.Usage`）；**评测报告没有**——`CaseResult` 无用量字段（`eval.go:158-198`），`Report` 只有 `PromptChars`（字符数，`eval.go:267`）；`runner.go:224` 拿到 `g` 却**丢掉 `g.Usage`** | ❌ **P1** |
| A10 四个「可」 | 可追踪 ✅（`--json` + 逐条 stderr）／可恢复 ⚠（手动）／可对账 ✅（`ID`）／可交付 ✅（基线 + 退出码）。**但 P0 不修则「可追踪」在一次 panic 下整体失效** | ⚠ |
| A11 闲时调度 | 单机没有「在线业务」要错峰，也没有计费对象 | ➖ |
| A12① 抽样 → 全量 | Gleam 没有「数据行」型任务；它的「全量」是用例集**本来就全跑** | ➖ |
| A12② 把个人经验固化成可重复规则 | 技能系统 + 规则表（`internal/agent/rules.go`，版本指纹 `RuleSet` 进了评测报告 `eval.go:239-241`） | ✅ 已由产品本体覆盖 |
| A12③ 评测变发布前常规步骤 | **比资料更深**：分层（smoke/regression/edge/adversarial/holdout）、保留池、可重复性、基线回归、badcase 回流、不可解用例独立口径 | ✅✅ |
| A13 从小批次起步 | `--layer smoke` 就是「先跑一小批」；`--strict` 门禁已在 `scripts/verify.sh:139` | ✅ |
| A14① 坏数据混入 | 前置校验挡住（A6） | ✅ |
| A14② 个别任务卡住整批 | ❌ 同 A7：一次 panic 带走整批 | ❌ **P0** |
| A14③ 节点中断丢状态 | `runs/` 只留没跑完的，无自动续跑（同 A7 第三行） | ⚠ |
| A14④ 结果对不上输入 | `CaseResult.ID` 可对 | ✅ |

**统计**：✅ 11 ｜ ⚠ 4 ｜ ❌ 3（收敛为 P0 ×2、P1 ×1）｜ ➖ 5

---

## 三、已经做对的（不要拆坏）

这一节不能省——**资料在这一块比 Gleam 粗**，照搬会拆掉资产。

### 3.1 评测工程：资料只讲到「变成常规步骤」，Gleam 已经做成了门禁

资料 A12③ 的全部内容是一句「把 Agent 评测变成发布前的常规步骤，让回归评测从临时工程变成可重复的发布流程」。
Gleam 的 `gleam eval` 已经超出它，且**每一层都附了理由**：

- **卡的是「回归」而不是「未过」**（`cmd/gleam/eval.go:252-268` `evalGateErr`）。理由写在注释里：「用例集里允许存在已知未过的用例（记录待修问题），一律卡会逼人把 `--strict` 整个去掉，门禁就废了」。**这是资料没有的一层。**
- **保留池（holdout）**：单条结果**不进调试视图**（`cmd/gleam/eval.go:291-294`），防止调参过拟合到看得见的用例。
- **可重复性单独一个数**：`--repeat N` 与通过率**并列但口径不同**（`internal/eval/runner.go:33-41`）——「通过率回答『对不对』，可重复性回答『稳不稳』」。
- **不可解用例独立口径**：识别成功不计入 `Passed`，理由是「混进通过率会激励模型对做不到的事硬猜『做完了』」（`runner.go:113-121`）。

**升级时的注意点**：P0/P1 的改动**不得改变通过率口径**——`runner.go:39-40` 明确「报告里的通过率仍然取**首次运行**的结果」，动了这条基线就要重新固定。

### 3.2 「可恢复」Gleam 用更省的方式实现了

资料 A7 说「执行节点异常可根据已保存的状态继续处理」，听起来像要求常驻的断点续跑。
Gleam 的做法是：**只留没跑完的**——终态快照写盘后立刻 `DiscardRunLog`（`cmd/gleam/main.go:534`、`internal/server/service.go:254`），注释写明「runs/ 里因此只留下**没跑完**的运行，正好是唯一需要它的那批」。已经跑完的任务不需要续跑凭据，留着只是占盘。

**升级时的注意点**：不要因为资料提了「可恢复」就去补一个常驻的断点续跑——那是**扩大**，不是补齐。

### 3.3 「不做完整断点续跑」是写下来的决定，不是漏洞

`cmd/gleam/pending.go:20-24` 的原话：「落盘 + 这个命令让『卡在审批』可见，人工确认后重新提交即可——**不做完整断点续跑**（桌面单机场景下收益未验证）」。
这正是负知识该有的样子：**外部资料说「应该能续」，Gleam 的答复是「收益未验证，不做，但写下来」**。不要因为资料说了就加。

### 3.4 前置校验比资料更严：在任何模型调用之前

资料 A6 只说「提交后先检查」。Gleam 的顺序是 `loadEvalCases`（含 `Validate`）在 `buildRuntime` **之前**（`cmd/gleam/eval.go:92` vs `:97`）——用例集写坏时**一分钱不花**就报出来。

### 3.5 对账键已经存在，而且比 `custom_id` 更稳

资料 A9 的 `custom_id` 是**平台生成/回填**的关联键。Gleam 用 `Case.ID`（`eval.go:159`）——它是**人写的、语义化的、受版本控制的**，改一条用例名在 diff 里看得见。不要为了「像平台那样」去引入一个机器生成的 ID。

### 3.6 任务级用量已有

`types.GoalResult.Usage`（`LLMCalls` / `PromptTokens` / `CompletionTokens` / `ToolCalls` / `Retries` / `Deduped` / `DurationMs`，累加于 `internal/agent/agent.go:698`）。P1 只是把它**接到评测报告上**，不是从零建。

---

## 四、差距清单（按 改动成本 × 影响面 排序）

### P0 · 一次 panic 会带走整批（对应 A7 + A14②）—— **已落地（批次 E → §4.6.27.1）**

**取证**

- `internal/agent/executor.go:359-421`：每个步骤一个 goroutine（`:365 go func(i int) {`），函数体内**没有 `defer recover`**；步骤实现在 `:405 state.results[i] = e.runStep(...)`。
- 全仓 `grep -rn "recover()" --include=*.go .` 只命中 `cmd/gleam/console_windows.go:17`（控制台初始化）——**执行路径零命中**。
- 因此 `e.runStep` 里任何 panic（工具实现、输出解析、MCP 响应处理、nil deref）都会**终止整个进程**。
- **关键**：panic 发生在**子 goroutine**，所以外层（`eval.Runner.Run` / `cmdGoal` / webui handler）**加 recover 也接不住**——修复点只能在 `executor.go:365` 那个 goroutine 的边界。

**为什么是问题**

资料 A14 说「批量最怕的不是慢，而是失控」，其中第二条就是「个别任务卡住整批」。Gleam 的形态比它更极端：

- `gleam eval --depth full` 会**真的调工具**。一次 panic 的代价不是「一条用例变红」，而是**整份报告消失**——已经通过的用例、已经花掉的 token 一起没了，重跑还要再花一次。
- 同时并发跑的其他任务一起死：webui 多任务、定时任务同时到点。
- 与仓库自己的设计意图冲突：Gleam 在**失败归因**上投入很多（`FailureKinds`、`Report.FailureBreakdown`、`eval.go:363-374`），而 panic 是**唯一一种绕过全部归因机制的失败**——它不留 `StepResult`、不进 `runs/`、不产生任何可统计的东西。

**改法**

1. 在 `executor.go:365` 的 goroutine 顶部加 `defer`，把 panic 转成**已有的 `types.StepFailed`**（写 `state.results[i]` + 调 `e.record`），`Error` 里带 panic 值与精简栈。
2. **不要静默吞掉**：步骤必须标记为 `StepFailed`（于是任务不会被判 success），错误文本带可识别前缀（如 `工具 panic:`），使 `--strict`、`FailureBreakdown`、`runs/` 三处都能看见它。**一个被吞掉的 panic 就是一次新的静默失效。**
3. **只加在这一处**：它覆盖 `eval` / `gleam goal` / webui / 定时任务四条路径，避免在四个调用点各写一遍（本仓库的「一个事实一个 owner」）。

**验收**

- 注入一个会 panic 的工具（或一条能触发 nil deref 的用例），断言：①整批仍跑完并产出报告；②该步骤状态为 `StepFailed` 且 `Error` 含 `工具 panic:`；③**同批其他用例的结果与不注入时逐条一致**。
- **负例控制**：把 recover 注释掉，同一个测试必须变红（进程崩 / 无报告）。否则这个断言测的是别的东西。

**落地时改掉原计划的两处**

1. **多了一条设计约束：收尾必须留在 guard 之外。** 原计划只说"加 defer recover"。实际动手时发现：goroutine 里 panic 之后，**defer 之外的剩余语句全部被跳过**——所以 `close(state.finished[i])`、落盘、计数、进度这四件收尾事如果写在 recover 那段里，依赖这一步的下游会**永远等不到放行**，症状是**任务挂住**（不是报错，比崩掉更难查）。因此新增 `runStepGuarded` 这个薄包装：panic 边界收在它内部，收尾留在外面的 goroutine 里——外面那部分**不可能被跳过**。并为此单独加了一条测试（`TestExecute_ToolPanicReleasesDownstream`，用显式 20 秒超时把"挂住"变成可读的失败信息）。
2. **归因单列了 `types.ErrInternal`，而不是塞进 `unknown`。** 归因分布的判据是"能不能回答该先修哪层"，而"要改代码"与"换方案再试"是两个完全不同的动作——混进 `unknown` 会让一次工具缺陷看起来像一批业务失败。定类走**结构**（执行器自己 recover 到的）而不是 `ClassifyError` 的文本启发式。

**落地时的新事实**：`panicBrief` 里的栈**截断到 7 行**并带 `工具 panic:` 前缀。带栈是因为 recover 之后 Go 不再打印任何东西，只留 panic 值的话"哪一行炸的"就丢了；截断是因为它会进 `tasks/*.json` 与 `runs/*.jsonl`，完整栈几十行会把任务记录淹掉。

**验证**：3 条测试；**变异 6 处全部会响**（去掉 recover / 标成成功 / 归因塞 unknown / 栈不截断 / 丢掉 panic 值 / panic 路径不释放下游），其中最后一条跑了 21 秒才红——正是设计中的症状。

---

### P1 · 评测报告缺「这批花了多少」（对应 A9 末句）—— **已落地（批次 F-1 → §4.6.27.2）**

**取证**

- `internal/eval/runner.go:224` `g := r.Agent.RunGoal(cctx, req)`——`g.Usage` 就在手上；`runCaseOnce` 只取了 `g.Steps` / `g.Status` / `g.Score` / `g.FailureBreakdown`（`:225-244`），**`g.Usage` 直接丢弃**。
- `internal/eval/eval.go:158-198` `CaseResult` 无任何用量字段；`eval.go:226-272` `Report` 只有 `PromptChars`（**字符数**）与 `RetryOK`。
- 结果：full 深度跑 200 条用例后，报告**无法回答「花了多少 token / 调了多少次模型」**。

**为什么是问题**

这不是新增原则，是**补完已有的原则**：`eval.go:175-177` 的注释已经写了「提示词成本与通过率必须记在同一行」。
`PromptChars` 是**成本代理**，token 用量是**成本本体**——代理有了，本体没有。
而评测的存在理由之一就是「给提示词做减法」（`cmd/gleam/eval.go:20-22`）：减法要算账，账现在只存在于终端回滚缓冲里。

**改法**

1. `CaseResult` 加用量字段（复用 `types.Usage` 或平铺），在 `runCaseOnce` 的 `DepthFull` 分支从 `g.Usage` 拷。
2. `Report` 加合计字段，在 `Run` 循环（`runner.go:66-81`）或 `tally`（`:106`）里累加。
3. 报告尾行打印，与 `PromptChars` 同一段呈现；`--json` 自然带上。
4. **口径必须写清**：`select` / `plan` 深度不执行，用量为 0——报告要写明「仅 full 深度有用量」，否则 0 会被读成「没花钱」。这正是仓库反复防的「分母/边界不说清」（见 `eval.go:349-362` 的口径三要素）。

**验收**

- `--depth full --mock-llm` 跑一条用例：断言 `--json` 里字段存在，且合计 == 各条之和；再断言 `select` 深度下该字段为 0 且报告有「仅 full 深度」的说明。
- 非零校验需要真实模型（无凭证环境下只验存在性与求和一致性，并在报告里注明）。

**落地时改掉原计划的三处**

1. **`select` 深度下字段是 `nil` 而不是 0**（原计划写的是"为 0 并说明"）。用指针区分"没计量"与"计量了但确实是 0"——一个 0 会被读成"计量了但没花钱"，而真相是"这个深度压根不计量"。报告对 select **不打印用量行**（表头已经写了"本地关键词筛选，未调用模型"），对 plan 才打印一句"未计量 + 为什么"。
2. **`--repeat N` 的每一遍都要算**（原计划没提）。重跑真的调了模型、真的花了钱；只报首遍会让 `--repeat 3` 看起来与 `--repeat 1` 一样贵——**那是账错了，不是省了**。通过率仍取首遍（口径可比），用量取全部遍数。
3. **另立 `eval.CaseUsage`，不复用 `types.TaskUsage`**（原计划写的是"复用 `types.Usage` 或平铺"）。前者带 `DurationMs`，而 `CaseResult` 已有 `DurationMS`——同一个报告里两个名字几乎一样、口径不同的耗时，读的人一定会拿错。转换点收敛在 `usageFromTask` 一处，`TaskUsage` 将来加字段必须在那里显式决定一次。

**落地时发现的新事实（原计划里没有）**：**`plan` 深度也会调模型**（规划那一次），但它的调用**不经过 `RunGoal`、没有 taskID 登记**，所以归集不到用例上。原计划写的是"select/plan 深度不执行，用量为 0"——**这句是错的**：plan 不是"不执行"，而是"执行了但没计量"。报告因此对 plan 明写「用量：未计量（plan 深度的规划调用不经过 RunGoal，没有归集到用例上）」——**这是缺口，不是 0**。要补上需要给 `Agent` 开两个公开方法（登记 + 读取），为一个次级深度的成本核算扩 API 不划算，先记为限制。

**验证**：4 条测试（其中 full 深度的桩**必须显式调用 `llm.ReportUsage`**，否则只会测出一个 0——那种绿是最坏的一种）；**变异 5 处全部会响**（不拷 Usage / 只算首遍 / 不汇总进报告 / 漏抄 CachedTokens / 抄错字段）。

---

### P2 · 归档任务在 UI 里读不回来（对应 A5④ + A10「可追踪」）—— **已落地（批次 F-2 → §4.6.27.3）**

**取证**

- `internal/webui/handlers.go:315-326` `handleGoalGet`：`t, ok := s.tasks[id]`，`if !ok { writeErr(w, 404, "任务 %q 不存在", id) }`——**只读内存**，不回落读盘。
- 档案确实在盘上：`cmd/gleam/main.go:527-538` 与 `internal/server/service.go:240-254` 都把终态写进 `<DataDir>/tasks/<TaskID>.json`。
- **同一个文件 CLI 能读**：`cmd/gleam/replay.go:294` `taskPath := filepath.Join(dataDir, "tasks", ref+".json")`。
- 内存表还有上限：`handlers.go:279` `maxRetainedTasks = 200`，`pruneTasksLocked`（`:283-300`）淘汰最旧的已完成任务；且 `s.tasks` 是纯内存（`internal/webui/webui.go:36`），**进程重启即空**。

**为什么是问题**

用户昨晚跑了 5 个任务，今早打开 Web UI：列表空的；拿 task id 去查，回 **404「任务不存在」**——而文件就在 `tasks/` 里躺着，`gleam replay <id>` 还能把它完整回放。
**同一个事实，两个入口给出相反答案**，这是仓库反复记录的那一类静默失效（数据在，界面说没有）。
更坏的是它**不像 bug**：404 是一个明确、自信的答复，用户会据此认为「任务没跑成 / 记录丢了」，然后重跑——重复花钱。

（与上一份清单 P0 的关系：那批修的是 `OnTaskDone` 的**写入侧补登记**（定时任务不在内存表里），这里修的是**读取侧回落**。两者相邻但不同。）

**改法（最小）**

1. `handleGoalGet` 内存 miss 时回落读 `tasks/<id>.json`，用 `GoalResult` 重建 `taskInfo`（`Goal` / `Status` / `Steps` / `Usage` / 时间戳够填）。
2. **不要**顺手让 `handleGoalList` 也读盘——「列表要不要显示历史、要不要分页」是另一个决定，先不做；`pruneTasksLocked` 的语义也不动。
3. 两边都没有时仍然 404，但措辞要能区分两种情况（「内存里没有，归档里也没有」）。

**验收**

- 真跑一个任务 → 重启进程（或手工清空 `s.tasks`）→ `GET /api/goals/{id}` 仍返回 200 且 `status` 与档案一致。
- **负例控制**：删掉 `tasks/<id>.json` 后必须回到 404。

**落地时补上的第三件事（原计划没写）**：**回落读盘带来了一个新的攻击面**。`id` 来自 URL 且会被拼进文件路径，所以 `archivedTask` 必须先挡 `..` 与路径分隔符——挡不住就等于把数据目录下的**任意 JSON** 变成一个可读接口（`settings.json` 里可能有密钥）。为它单独加了一条测试。

**落地时发现的新事实**：**档案与内存的两处差异是结构性的，不是缺陷**。`GoalResult` 里**没有 `Mode` 字段**（模式不参与结果，只影响怎么跑），所以归档任务的 `mode` 为空；`Events` 是 SSE 事件流，进程退出即散，所以归档任务的时间线是空的——但 `Result.Steps` 是完整的，"跑到哪一步、哪一步失败"仍然查得到。**时间线空不等于过程丢了**，这两句话都写进了 `archivedTask` 的注释，免得下一个人把它当 bug 去"修"。

**测试设计上踩的一个坑（值得记）**：路径穿越那条测试的诱饵文件最初写成了 `<dataDir>/settings.yaml`，而穿越路径 `tasks/../settings` 拼出来的是 `settings.json` ——**解析不到**，于是接口照样回 404，**那条断言是空的**（改坏守卫也不会响）。改成 `<dataDir>/settings.json` 后，"守卫没挡住"才会真的读到一个文件。另外测试同时**直接调 `archivedTask`**，因为路由本身可能先挡掉一部分形态，只测 HTTP 的话守卫本身可能测不到。

**验证**：4 条测试；**变异 3 处全部会响**（去掉归档回落 / 读不到也回 200 / 去掉路径穿越守卫）。

---

### P3 · 「max_concurrency 是单任务的，不是全局的」要写下来（对应 A8）—— **已落地（批次 G → §4.6.27.4）**

**取证**

- `internal/agent/executor.go:337`：`sem := make(chan struct{}, conc)` 建在**每次计划执行**里；`conc` ← `e.MaxConcurrency` ← `internal/agent/agent.go:929/1574` ← `a.Cfg.Agent.MaxConcurrency`（默认 **8**，`internal/config/config.go:142`）。
- 也就是说：**N 个任务并发时，同时在跑的工具调用上限是 N×8，不是 8。**
- 调度器不设任务级上限：`internal/harness/scheduler/scheduler.go:210` 对每个到期 job 直接 `go fire(j)`（`checkWatch` 的 `:228`、`:387` 同理）。3 个定时任务同时到点 = 3 个完整 agent 各自 8 路工具并发。

**为什么只写文档、不改代码**

- 加全局调度队列是**架构变更**（要引入队列、公平性、优先级），而单机场景下「3 个定时任务撞在一起」的收益**未验证**——与 `pending.go:20-24` 里「不做完整断点续跑，收益未验证」是同一类判断。
- 但 `max_concurrency` 这个名字**会被误读成全局**：用户按 8 去估算本机负载，实际可能是 24。这是「**可能被误读的事实**」，正是 `docs/known-limits.md` §一 该收的东西（同类先例：「场景模板的 tools 是提示不是白名单」）。

**改法（已落地）**：`docs/known-limits.md` §一 增一行，紧跟同类先例「场景模板的 tools 是提示不是白名单」之后：

```
| max_concurrency 只管单任务，不是全局 | 每次执行各建一个信号量，N 个任务并发就是 N×8 |
```

原计划的措辞是「是单任务的，不是全局上限 | N 个任务并发时实际是 N×8（每次执行各建一个信号量）」，**落地时压短了**——那一版 61 字符，超了每行 ≤60 的预算 1 个字符。压缩后 58 字符，并且把"为什么"（每次执行各建一个信号量）放在了理由列，比原稿更直接。

**验收**：`scripts/check-doc-budget.py` 报 `docs/known-limits.md 2386 / 2640`、**无超长行**、全部在预算内；这一行 `grep` 得到。

---

## 五、建议的落地顺序

| 批 | 内容 | 为什么这个顺序 |
| :--- | :--- | :--- |
| **批次 E**（✅ 已落地） | **P0**（executor 的 panic 边界 + 测试） | 改动只有一处 + 一个测试，但它保护的是**所有**批（评测 / 多任务 / 定时任务）。**必须先修它**：一次 panic 会让所有统计失真，后面的观测建立在一个会突然消失的底座上 |
| **批次 F**（✅ 已落地） | **P1 + P2**（评测用量 / 归档读回） | 都是「让已有的东西可见 / 可读回」，互不依赖，可并行。P1 是补完已有原则，P2 是消除「同一事实两个答案」 |
| **批次 G**（✅ 已落地） | **P3**（known-limits 一行） | 顺手做，与 F 同批 |

三批**都不碰持久化格式**（不改 `tasks/*.json`、不改 `runs/*.jsonl` 的字段），所以没有迁移风险。

**三批的实际验证（2026-09-23）**：全量测试 **27 个测试包全过**；新增 **11 条测试**；变异 **14 处全部被断言看见**（P0 六处、F 八处）；评测基线 `eval --baseline internal/eval/baseline-select.json --strict` **退出码 0、16/16 通过、无回归**；`gofmt -l` 输出为空；文档预算全部在范围内。

> 与上一份清单的批次编号（C / D）续接：本份从 **E** 起。

---

## 六、明确不做（附理由）

| 不做 | 理由 |
| :--- | :--- |
| **通用批量提交接口**（JSONL 输入 / `POST /api/goals/batch`） | 资料的场景是「业务系统把 10 万条任务写进 JSONL 交给平台」。Gleam 是**单机单用户**，用户就坐在键盘前，没有「从上游系统接收任务流」这回事。真正需要「批」的地方只有评测，而评测已经有 `--cases` + 分层 + 基线。加一个通用批接口 = 为不存在的场景建一套架构（还要带上幂等、配额、对账），正是上一份清单里「不要因为别人的架构更大就跟着变大」的同一条 |
| **行级隔离**（坏一行跳过、其余继续） | **这条方向相反。** 资料的「行」是**数据**（客服会话、合同），坏一行是脏数据，跳过是对的。Gleam 的「批」是**用例集**——受版本控制的代码资产，坏一条是**测试集自身的 bug**。静默跳过会让评测**悄悄少跑一条而通过率看起来没变**，正是 `internal/eval/cases.go:63-66` 注释里防的那件事（「没有任何期望的用例**永远会通过**……评测最容易被这样悄悄架空」）。保持 `Validate` 遇第一条即 return（`cases.go:67-92`） |
| **闲时调度 / 错峰折扣** | 云上错峰是为了不和**在线业务**抢资源，折扣只影响 Credits 计费。单机没有在线业务要避让，也没有计量对象。资料 A11 自己也说「折扣仅影响 Credits 计价，模型质量不变」——对 Gleam 是空集 |
| **`output.jsonl` / `error.jsonl` 分文件** | `--json`（`cmd/gleam/eval.go:170-175`）已给出**完整**的机器可读报告（含每条用例的 `passed` 与逐条 `checks`），失败项 `jq` 一条就筛出来。再分两个文件是同一份数据的第二种形状，会立刻产生「两处各写一遍、然后漂移」的问题（本仓库的老坑） |
| **单批 10,000 项的上限** | Gleam 的用例集是人工维护的几十条（`internal/eval/cases.json`），没有需要这个上限的场景 |
| **自动断点续跑** | 已在 `cmd/gleam/pending.go:20-24` 明确记录为「桌面单机场景下收益未验证」的**刻意不做**，本次不推翻。**但**：P1 把用量接到报告上之后，「续跑省下的钱」第一次变得**可度量**——将来若要重新评估，凭据就在报告里。这比现在拍脑袋决定更靠谱 |
| **`identity_id` / 多租户 / Template 引用** | 已由 `Qoder Cloud Agents 对照与升级清单.md` §六 判定不做：单机单用户无租户概念；「方法复用」已由场景模板 + 技能系统覆盖 |

---

## 附录：本次分析的取证边界

- **原文**：`curl -sL` + UA 抓取（HTML 3.5 MB）→ 抽 `id="js_content"` 正文（**8861 字**，命中位置 561 KB 处），逐段精读。A1…A14 判据表与全部数字（10,000 / 22:00–08:00 / 01:00–07:00 / 50–60 万 / 2026-09-21）均取自原文。
- **三路并行调研**（按本篇主题重推分片轴，未复用上一篇的轴）：
  - A · 批量执行与任务编排 → `cmd/gleam/app.go`、`main.go`、`internal/webui/`、`internal/server/`、`internal/agent/executor.go`、`agent.go`、`internal/harness/scheduler/`
  - B · 输入契约与行级校验 → `pkg/types/types.go`、`internal/agent/agent.go` `ValidateGoalRequest`、`internal/eval/cases.go`、`internal/config/`
  - C · 结果收集 / 对账 / 评测 → `cmd/gleam/eval.go`、`internal/eval/*`、`internal/agent/runlog.go`、`cmd/gleam/replay.go`、`internal/harness/growth/`
- **逐行复核过的取证**（写进本清单的每一处都能在原文指出那一行）：
  `internal/agent/executor.go:337`（信号量建在每次执行里）、`:365`（步骤 goroutine）、`:405`（runStep 调用点）；
  `grep -rn "recover()" --include=*.go .` → 仅 `cmd/gleam/console_windows.go:17`；
  `internal/eval/runner.go:66-81`（顺序循环）、`:106-137`（tally）、`:224`（丢 `g.Usage`）；
  `internal/eval/eval.go:158-198`（CaseResult）、`:226-272`（Report）、`:349-362`（口径三要素）；
  `internal/eval/cases.go:35-47`、`:63-66`、`:67-92`；
  `cmd/gleam/eval.go:20-32`、`:92-101`、`:128-143`、`:170-181`、`:252-268`、`:291-294`；
  `internal/webui/handlers.go:231-238`、`:251`、`:261`、`:279-300`、`:315-326`；`internal/webui/webui.go:36`；
  `internal/server/service.go:240-254`；`cmd/gleam/main.go:510-514`、`:527-538`；
  `cmd/gleam/replay.go:294`；`cmd/gleam/pending.go:20-24`；
  `internal/harness/scheduler/scheduler.go:210`、`:228`、`:387`；
  `internal/agent/runlog.go:44`、`:137`；`internal/config/config.go:142`；`scripts/verify.sh:139`。
- **修正过子代理的一处结论**：B 路报告「不存在『部分成功』的表达」。**在评测这条批上不成立**——`Report` 有 `Passed / Failed / Known` 三档（`eval.go:242-246`），`CaseResult` 逐条记录，`tally`（`runner.go:106-137`）负责归集。子代理找的是**任务级批量接口**的部分成功，而 Gleam 的「批」在评测里。这条不写进差距清单。
- **未做**：未运行任何评测（无 LLM 凭证）；**未改动任何源码**——P0…P3 均为提案，等拍板。
