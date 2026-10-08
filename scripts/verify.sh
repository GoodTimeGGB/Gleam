#!/usr/bin/env bash
# Gleam 仓库的统一验证闸门：六层，任一层失败即 exit non-zero。
#
# 为什么要有这个：**规则接到执行**——agent 做错了立刻被机器拦住，这是最高杠杆的一步。
# 在此之前守卫都在，但没有统一入口：每次要手敲 4~7 条命令，漏一条没人知道，
# 而且「改了这里该跑哪条」只存在于人的记忆里。映射表在 `AGENTS.md`。
#
# 六层各证明一件事，**按成本从低到高**排（让最便宜的检查最先撞墙）：
#   1 格式与引用 gofmt + §ref —— 文本层的机器可读约定（代码文本 + 文档文本）
#   2 静态   go vet         —— 可疑构造（无用赋值、可疑的类型转换、错用的锁……）
#   3 编译   go build       —— 语法与类型自洽
#   4 行为   go test        —— 单元 / 集成测试全绿
#   5 端到端 smoke          —— 真实二进制、真实文件路径跑得通
#   6 就绪   doctor + eval  —— 配置与数据目录就绪；评测基线不倒退
#
# 资料给的顺序是「编译 → 解析 → …」，这里改成**成本序**，是有意的：
# 层数与各层职责不变，但最便宜的检查先拦人。
#
# 最后一步是**语义 review**（人类步骤）：三问自检 + 文档字数预算报告。
# 它不是命令，所以**不参与退出码**——但会打印出来，逼着人看一眼。
#
# 另外有个**第 0 步 · 闸门自检**（不占层号）：跑 scripts/lib/gonoise_probe.sh，
# 证明 go_retry 只吃环境噪声、不掩盖真实失败。理由见那里的注释——
# 「闸门自己也会坏」，而坏掉的闸门比没有闸门更坏。
#
# ⚠ 第 0 步**不在上面的成本序里**：它是分钟级（本机实测 1~3 分钟，比第 1 层贵一个量级），
# 因为它全是进程创建，而本机进程创建是秒级的（1.5~2.7 秒/次，见 docs/known-limits.md）。
# 它仍然排在最前面，不是因为便宜，而是因为它是**前置条件**：尺子坏了，后面每层的结论都不可信。
#
# 用法：
#   bash scripts/verify.sh           # 六层全跑
#   bash scripts/verify.sh --quick   # 只到第 4 层（内循环用，跳过端到端与就绪）
#
# 本脚本**不用 `set -e`**：哪一层失败由显式判断说清，而不是让 shell 悄悄退出。
# （`set -e` 对 `A && B` 形式的失败不生效，靠它守门会得到"永远通过"的闸门。）
set -uo pipefail
cd "$(dirname "$0")/.."

QUICK=0
for arg in "$@"; do
  case "$arg" in
    --quick) QUICK=1 ;;
    # 用法 = 文件头部那段注释，**自动推导**而不是写死行号：
    # 原来写的是 `sed -n '2,28p'`，于是"注释块有多长"这个事实被写了两遍——
    # 往头部加几行说明，`--help` 就会**悄悄截掉「用法」段**（真发生过一次）。
    -h|--help) awk 'NR>1 { if ($0 ~ /^#/) { print; next } ; exit }' "$0"; exit 0 ;;
    *) echo "未知参数：$arg（可用：--quick）"; exit 2 ;;
  esac
done

LAYER=0
TOTAL0=$(date +%s)

# Go 工具链环境噪声的统一处理（判据只写一份，见脚本头部注释）。
# shellcheck source=lib/gonoise.sh
source scripts/lib/gonoise.sh

# python 解释器只解析一次，给两处用：第 1 层的文档引用检查（硬门禁）与末尾的
# 文档字数预算（告警）。两处各解析一遍，迟早一处改了另一处没改。
PY=""
for c in python3 python py; do
  if command -v "$c" >/dev/null 2>&1; then PY="$c"; break; fi
done

step() { # step <层名> <命令…>
  LAYER=$((LAYER + 1))
  local name="$1"
  shift
  local t0 t1
  echo ""
  echo "──────── 第 $LAYER 层 · $name ────────"
  t0=$(date +%s)
  if "$@"; then
    t1=$(date +%s)
    echo "  ✔ 通过（$((t1 - t0))s）"
    return 0
  fi
  t1=$(date +%s)
  echo "  ✘ 失败（$((t1 - t0))s）"
  echo ""
  echo "===== 验证失败：第 $LAYER 层「$name」 ====="
  return 1
}

# ── 第 1 层 · 格式与引用 ──
#
# 这些都归这里：**都是文本层的机器可读约定，都便宜、都不需要编译**。
# 按成本序，它们必须和 gofmt 一起最早撞墙。
#
#   * gofmt：代码文本。`gofmt -l` 的"输出非空"才是失败信号，所以先接住输出再判断，
#     别把"输出为空"和"命令本身没跑起来"混成同一件事。
#   * §引用：文档文本。悬空引用是**静默失效**——读者跟着指针走到空处，不会报错，
#     只会得出"文档里没写"的结论（Markdown 没有链接检查器管它）。
#   * 落盘写法：代码文本。状态写入必须走 internal/atomicfile——`os.WriteFile` 失败时
#     留下的是**半截 JSON**，下次打开解析失败，丢的是整份状态而不是一条记录。
#     这种缺陷只在崩溃之后显形，写的时候代码看着完全正常，所以只能靠闸门拦。
#   * 接口清单：文档与代码对齐。README 是"用户可见能力"的 owner，清单漏一条等于
#     这件事对下一个人不存在；多写一条更坏——那是照着调会拿到 404 的假能力。
#   * 前端启动段：app.js 的启动序列必须在所有顶层声明之后。暂时性死区的报错不打断
#     渲染，坏掉的只是那几块静默为空——正是"数据在，界面说没有"的 JS 版。
#   * DOM 锚点：JS 引用的每个 id 都要有地方定义。`$('#x')` 拿到 null 不抛错，
#     那块区域就此永远空白，和上面那条是同一个缺陷的两个方向（一个查声明顺序，一个查拼写）。
#   * 版本口径：前端不许再抄一份版本号，也不许替「有没有新版」下结论。判据的字面量是从
#     internal/buildinfo 读的，所以升版本时它自动跟着挪，不必有人记得改脚本。
#   * 出网台账：新增一条会往外发的路径，必须同时在台账清单里登记。台账是「我允许了什么」
#     的唯一出口——漏登记的那条不是"少一行显示"，是**这张表说的"全部"当场变成假话**。
#
# 为什么 §引用 归这一层、而不是新加一层：层数一变，"六层"这个说法在 AGENTS.md / README /
# 设计文档 / 技能里全要改；而它本来就是"文本层的格式约定"，第 1 层是它的正确归属
# （先问归属，再谈实现）。落盘写法、接口清单同理。
text_layer() {
  local out
  out=$(gofmt -l ./internal ./pkg ./cmd)
  if [ -n "$out" ]; then
    echo "  以下文件未格式化（跑 gofmt -w 修）："
    printf '%s\n' "$out" | sed 's/^/    /'
    return 1
  fi
  echo "  gofmt 干净：./internal ./pkg ./cmd"

  if [ -z "$PY" ]; then
    # **静默跳过是这里最坏的选择**：一个"没跑但报通过"的检查比没有检查更坏
    # （它给出的是"已验证"的错觉）。所以找不到解释器就算这一层没过，并说清怎么办。
    echo "  找不到 python（python3 / python / py 都没有）：文档引用检查无法执行。"
    echo "  **这一层不算通过**——会静默跳过的检查等于没有检查。装一个 python 后重跑。"
    return 1
  fi
  "$PY" scripts/check-doc-refs.py . || return 1
  "$PY" scripts/check-atomic-write.py . || return 1
  "$PY" scripts/check-api-docs.py . || return 1
  # --self-test：顺手拿两份坏文本（尾巴挂语句 / 标记丢了）确认这道判据真会拦，
  # 而不是只在真文件上点头。
  "$PY" scripts/check-app-startup.py --self-test . || return 1
  # 闸门脚本的中文输出必须是 UTF-8 字节，否则在终端与日志里就是乱码——读不懂的告警
  # 等于没有告警（本机 cp936 上真发生过）。判据自带负例，同样用 --self-test 跑。
  "$PY" scripts/check-py-utf8.py --self-test . || return 1
  # DOM 锚点：JS 里 $('#x') 指向的 id 必须真的存在。"数据在，界面说没有"的另一半——
  # 拿到 null 不报错，只是那块静默是空的，正好是最难发现的那种缺陷。
  "$PY" scripts/check-dom-anchors.py --self-test . || return 1
  # 版本口径：抄一份版本号＝升版本时漏改一处就对外报旧号；替「有没有新版」下结论＝
  # 把本机问不出的事讲成答案，比留空更坏（用户会照着它停止行动）。判据自带负例与干扰文本。
  "$PY" scripts/check-version-owner.py --self-test . || return 1
  # 出网落点与台账清单必须双向对上：字面量 kind 没登记、登记了却没人记、kind 传变量、
  # 文案里带 markdown 强调（那张表走 textContent，星号原样上界面）——四种都会让这张表说的
  # "全部"变成假话，而界面看着还是完整的。判据自带负例与干扰文本。
  "$PY" scripts/check-egress-owner.py --self-test . || return 1
  # 候补目标三处清单必须互相对上：cueSignals 登记了、deriveCues 派生了、cueSignalText 给了
  # 人话牌子——少一处就是界面永远不会出现那一类，或那一类裸奔成枚举值。另两条管"提议层不许
  # 伸手"（这一层没有执行入口）与文案不带 markdown（卡片走 textContent）。判据自带负例。
  "$PY" scripts/check-cue-owner.py --self-test . || return 1
}

# ── 第 5 层 · 端到端 ──
# 两个脚本都必须真的会失败：`smoke.sh` 用 ok/bad 计数（它以前是"永远通过"的），
# `smoke-replay.sh` 用 check/fail 标志。这里只按退出码判断。
smoke_layer() {
  bash scripts/smoke.sh || return 1
  bash scripts/smoke-replay.sh || return 1
}

# ── 第 2/3/4 层 · 走 go_retry（带环境噪声重试，实现见 scripts/lib/gonoise.sh） ──
vet_layer()   { go_retry "go vet"   go vet ./...; }
build_layer() { go_retry "go build" go build ./...; }
test_layer()  { go_retry "go test"  go test ./... -count=1 -timeout 300s; }

# ── 第 6 层 · 就绪与评测回归 ──
# 仍然**不用 `go run`**：本机实测它会偶发解析不到标准库（5 次里中 1 次）。
# 但根因不是 `go run` 特有——那是 Go 工具链的环境噪声，`go build`/`go test` 都会中，
# 所以统一走 `go_retry`（判据只在 scripts/lib/gonoise.sh 里写一份）。
# 这一层额外先建二进制再调用它：二进制建一次跑两次，比 `go run` 少一次工具链往返。
ready_layer() {
  go_retry "go build" go build -o bin/gleam.exe ./cmd/gleam || return 1
  ./bin/gleam.exe doctor --strict || return 1
  ./bin/gleam.exe eval --layer smoke --strict || return 1
}

# ── 第 0 步 · 闸门自检（不占层号，但失败即 exit）──
#
# 为什么要有：本闸门刚加了一层"环境噪声重试"（go_retry）。**重试逻辑本身也要被证明**——
# 它必须只吃那一签名、不掩盖真实失败，而且在带 `set -e` 的调用方里真的会终止脚本。
# 这三条都不是"看着对"就够的：写这个函数时第一版用了裸赋值 `out=$(cmd)`，
# 在 `set -e` 下会直接退出脚本，重试逻辑一次都跑不到。
# **它不便宜**：跑四次场景、每次若干断言，全是进程创建；而本机每次进程创建约 2.7 秒
# （实测 40 次 `$(cat …)` = 107 秒，见 docs/known-limits.md），所以探针实测是**分钟级**。
# 放在最前面**不是因为"最便宜的先撞墙"**，而是因为它是**前置条件**：
# 重试逻辑坏了，后面每一层的结论都不可信——先证明尺子，再量东西。
# （早期这里写的是"探针本身 <1s"，那个数字是错的，已改。）
bash scripts/lib/gonoise_probe.sh || {
  echo ""
  echo "===== 验证失败：闸门自检（go_retry 的行为与判据不符） ====="
  exit 1
}

step "格式与引用（gofmt + §ref）" text_layer || exit 1
step "静态检查（go vet）" vet_layer || exit 1
step "编译（go build）" build_layer || exit 1
# -timeout 是必需的：某条测试若死等，默认 10 分钟才 panic，闸门会看起来像卡住了。
step "行为（go test）" test_layer || exit 1

if [ "$QUICK" -eq 0 ]; then
  step "端到端（冒烟）" smoke_layer || exit 1
  step "就绪与评测回归（doctor / eval）" ready_layer || exit 1
fi

# ── 语义 review：人类步骤，不参与退出码 ──
echo ""
echo "──────── 语义 review（人类步骤，不影响退出码） ────────"

if [ -n "$PY" ]; then
  "$PY" scripts/check-doc-budget.py . || true
else
  echo "  未找到 python，跳过文档字数预算检查"
fi

cat <<'EOF'

  三问自检（完整答案在 docs/known-limits.md 头部）：
    1. 知识外置了吗？      这条约束如果只在我脑子里，下次它就不存在。
    2. 正确入口明确吗？    新人只读 AGENTS.md，能不能找到该改哪。
    3. 错误何时被发现？    有没有一条 exit non-zero 的命令，在最近的地方拦住它。
EOF

TOTAL1=$(date +%s)
echo ""
if [ "$QUICK" -eq 1 ]; then
  echo "===== 验证通过：$LAYER 层（--quick，未跑端到端与就绪），共 $((TOTAL1 - TOTAL0))s ====="
else
  echo "===== 验证通过：六层全过，共 $((TOTAL1 - TOTAL0))s ====="
fi
