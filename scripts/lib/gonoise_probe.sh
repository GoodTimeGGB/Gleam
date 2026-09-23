#!/usr/bin/env bash
# go_retry 的探针：证明四件事，缺一条就等于闸门在说谎。
#
#   ① 噪声一次 + 第二次成功  → 必须 rc=0，且**必须打印**（静默重试等于藏起来）
#   ② 真实失败（无噪声签名） → 命令**只被调用 1 次**（确定性失败不该被重试掩盖）
#   ③ 噪声两次 + 第三次成功  → 必须 rc=0（噪声是"一段窗口"，不是"一次调用"）
#   ④ 噪声一直存在         → 必须 rc=1、且尝试次数有上限（不无限重试）
#
# ③ 是这次新增的：原来的策略是"重试一次"，实测被证伪——首次 32 秒红、紧接着的重试**也红**，
# 两次命中的还是**不同的** std 包。所以"噪声属于一次调用"这个假设是错的，
# 它属于**一段几十秒的窗口**；①只证明了"能救一次"，③才证明"能熬过窗口"。
# 而 ④ 比 ①③ 更重要：只验重试成功，等于只证明了"失败会被吞掉"这条路径是通的。
#
# 本探针自己带 `set -e`，因为真正的坑就在这里：裸赋值 `out=$(cmd)` 在 set -e 下
# 会直接退出脚本，重试逻辑一次都跑不到——而症状只是"脚本静默结束"。
#
# 计数用**文件**而不是变量：`$(...)` 会 fork 子 shell（go_retry 内部那次 `out=$(...)`
# 也是），变量在子 shell 里加完就随子 shell 一起没了——探针会误报"命令没被调用"。
# 这是写探针时踩到的第一个坑，记在这里免得下次再踩。
#
# 用法：bash scripts/lib/gonoise_probe.sh
set -uo pipefail
cd "$(dirname "$0")/../.."

# 把重试间隔压到 0：本探针要跑四次、每次可能重试两回，等 3 秒会把第 0 步拖到十几秒。
# 等待本身是 `sleep` 一行，没有可证伪的行为；要证的是**次数与判定**。
export GLEAM_GO_FLAKE_BACKOFF=0
source scripts/lib/gonoise.sh

COUNT_FILE=$(mktemp)
echo 0 > "$COUNT_FILE"
bump() { echo $(( $(cat "$COUNT_FILE") + 1 )) > "$COUNT_FILE"; }
calls() { cat "$COUNT_FILE"; }

# 造一条与真实噪声同签名的输出（照抄实测形态，别改措辞）。
FLAKE_LINE='C:\Go\src\runtime\error.go:9:2: package internal/bytealg is not in std (C:\Go\src\internal\bytealg)'

fake_noise_once() {
  bump
  if [ "$(calls)" -eq 1 ]; then printf '%s\n' "$FLAKE_LINE" >&2; return 1; fi
  echo "build ok"
}

fake_real_fail() {
  bump
  echo "internal/agent/spill_test.go:41:2: undefined: nope" >&2
  return 1
}

fake_noise_twice() {
  bump
  if [ "$(calls)" -le 2 ]; then printf '%s\n' "$FLAKE_LINE" >&2; return 1; fi
  echo "build ok"
}

fake_noise_always() {
  bump
  printf '%s\n' "$FLAKE_LINE" >&2
  return 1
}

FAIL=0
report() { # report <期望RC> <实际RC> <期望调用次数> <实际调用次数> <说明>
  local want_rc="$1" got_rc="$2" want_calls="$3" got_calls="$4" desc="$5"
  if [ "$got_rc" -eq "$want_rc" ] && [ "$got_calls" -eq "$want_calls" ]; then
    echo "  ✓ $desc"
  else
    echo "  ✗ $desc（rc 期望 $want_rc 实得 $got_rc；调用次数期望 $want_calls 实得 $got_calls）"
    FAIL=1
  fi
}
# 跑一次 go_retry 并把 rc 与合并输出取回来（子 shell 里跑，所以计数走文件）。
run_probe() { # run_probe <说明> <命令名> → 打印到 PROBE_OUT / 设置 PROBE_RC
  PROBE_OUT=$( { go_retry "$1" "$2"; echo "__rc=$?"; } 2>&1 )
  PROBE_RC=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/^__rc=//p')
  PROBE_OUT=$(printf '%s\n' "$PROBE_OUT" | sed '/^__rc=/d')
}

echo "===== 探针 ① 噪声一次后成功 ====="
echo 0 > "$COUNT_FILE"
run_probe "探针①" fake_noise_once
printf '%s\n' "$PROBE_OUT" | sed 's/^/    /'
report 0 "$PROBE_RC" 2 "$(calls)" "噪声后重试成功，rc=0、命令调用 2 次"
printf '%s\n' "$PROBE_OUT" | grep -q "Go 工具链环境噪声" \
  && echo "  ✓ 重试被打印出来了（没有静默）" \
  || { echo "  ✗ 重试没有打印，等于把闸门抖动藏起来了"; FAIL=1; }

echo "===== 探针 ② 真实失败不被重试 ====="
echo 0 > "$COUNT_FILE"
run_probe "探针②" fake_real_fail
printf '%s\n' "$PROBE_OUT" | sed 's/^/    /'
report 1 "$PROBE_RC" 1 "$(calls)" "真实失败 rc=1、命令只调用 1 次"
printf '%s\n' "$PROBE_OUT" | grep -q "undefined: nope" \
  && echo "  ✓ 真实失败原因被透传（tail 输出）" \
  || { echo "  ✗ 真实失败原因没有打出来"; FAIL=1; }
printf '%s\n' "$PROBE_OUT" | grep -q "环境噪声" \
  && { echo "  ✗ 真实失败被当成噪声了（这会把真红变绿）"; FAIL=1; } \
  || echo "  ✓ 真实失败没有被误认成噪声"

echo "===== 探针 ③ 噪声两次后成功（熬过窗口） ====="
echo 0 > "$COUNT_FILE"
run_probe "探针③" fake_noise_twice
printf '%s\n' "$PROBE_OUT" | sed 's/^/    /'
report 0 "$PROBE_RC" 3 "$(calls)" "噪声两次后第三次成功，rc=0、命令调用 3 次"
printf '%s\n' "$PROBE_OUT" | grep -q "第 2 次尝试失败" \
  && echo "  ✓ 每次重试都打印了（两次警告）" \
  || { echo "  ✗ 重试没有逐次打印"; FAIL=1; }

echo "===== 探针 ④ 噪声一直存在（不无限重试） ====="
echo 0 > "$COUNT_FILE"
run_probe "探针④" fake_noise_always
printf '%s\n' "$PROBE_OUT" | sed 's/^/    /'
report 1 "$PROBE_RC" 3 "$(calls)" "噪声一直存在 rc=1、命令调用 3 次（有上限）"
printf '%s\n' "$PROBE_OUT" | grep -q "仍失败" \
  && echo "  ✓ 明确告知重试也没救回来" \
  || { echo "  ✗ 没有告知重试结果"; FAIL=1; }

echo ""
if [ "$FAIL" -ne 0 ]; then
  echo "===== 探针失败：go_retry 的行为与判据不符 ====="
  exit 1
fi
echo "===== 探针全过：重试只吃噪声，真实失败照样红 ====="
