#!/usr/bin/env bash
# Gleam 仓库的统一验证闸门：六层，任一层失败即 exit non-zero。
#
# 为什么要有这个：**规则接到执行**——agent 做错了立刻被机器拦住，这是最高杠杆的一步。
# 在此之前守卫都在，但没有统一入口：每次要手敲 4~7 条命令，漏一条没人知道，
# 而且「改了这里该跑哪条」只存在于人的记忆里。映射表在 `AGENTS.md`。
#
# 六层各证明一件事，**按成本从低到高**排（让最便宜的检查最先撞墙）：
#   1 格式   gofmt + §ref  —— 文本层的机器可读约定（代码文本 + 文档文本）
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
    -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
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
# 两件事都归这里：**都是文本层的机器可读约定，都便宜、都不需要编译**。
# 按成本序，它们必须和 gofmt 一起最早撞墙。
#
#   * gofmt：代码文本。`gofmt -l` 的"输出非空"才是失败信号，所以先接住输出再判断，
#     别把"输出为空"和"命令本身没跑起来"混成同一件事。
#   * §引用：文档文本。悬空引用是**静默失效**——读者跟着指针走到空处，不会报错，
#     只会得出"文档里没写"的结论（Markdown 没有链接检查器管它）。
#
# 为什么 §引用 归这一层、而不是新加一层：层数一变，"六层"这个说法在 AGENTS.md / README /
# 设计文档 / 技能里全要改；而它本来就是"文本层的格式约定"，第 1 层是它的正确归属
# （先问归属，再谈实现）。
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
# 探针本身 <1s、不碰 Go 工具链，所以放在最前面：最便宜的检查最先撞墙。
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
