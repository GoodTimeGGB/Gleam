#!/usr/bin/env bash
# Go 工具链环境噪声的统一处理。被 verify.sh / smoke.sh / smoke-replay.sh 共用。
#
# **为什么是共用的一份**：三个闸门脚本都要调 `go build`/`go test`。三处各写一遍重试逻辑，
# 迟早只在一处加、另两处漏——而漏掉的那处会继续偶发变红，然后整个闸门被忽略。
# 「一个事实一个 owner」在这里的具体含义就是：噪声的判据只写一次。
#
# 症状长这样（与代码无关，同一条命令重跑就好）：
#   C:\Go\src\runtime\error.go:9:2: package internal/bytealg is not in std (C:\Go\src\internal\bytealg)
# 实测命中面：`go run`（5 次里 1 次）、`go test`（跑到第 4 层 16 秒就红）、
# `go build`（smoke 的构建步骤）。它砸在哪一层是随机的，所以处理要统一。

GO_FLAKE_RE='is not in std \('

# 最多尝试几次（含首次）。默认 3。
GO_FLAKE_ATTEMPTS="${GLEAM_GO_FLAKE_ATTEMPTS:-3}"
# 两次尝试之间的间隔秒数。默认 3。
GO_FLAKE_BACKOFF="${GLEAM_GO_FLAKE_BACKOFF:-3}"

# go_retry <说明> <命令…>
#
# 只在上面那**一个签名**上重试，并且**必须打印**每一次重试。
#
# **为什么不是「重试一次」**：原来的假设是"噪声属于一次调用，立刻重跑就好"。
# 实测被证伪过一次——第 4 层首次跑 32 秒后红，紧接着的重试**也红**，而且两次命中的
# 是**不同的** std 包（`internal/stringslite` / `internal/poll` / `internal/syscall/windows`）。
# 也就是说噪声不是一个点，而是**一段几十秒的窗口**：窗口里任何一次 `go` 调用都可能红。
# 于是策略改成"最多 N 次尝试 + 中间等一会儿"，让它有机会熬过窗口。
#
# **为什么这不会掩盖真实失败**：该签名只可能由工具链解析不到自己的标准库产生，
# 任何测试代码都造不出它；而真实失败是**确定性**的——重试 N 次照样红（而且**一次都不会重试**，
# 因为它根本不带这个签名）。所以"多试几次"只可能把假红变绿，不可能把真红变绿。
#
# **为什么不是无限重试**：①环境持续坏下去时闸门要如实红，不能靠重试把问题拖过去；
# ②每次尝试都是完整跑一层（第 4 层 30–80 秒），次数越多假红时的等待越久。
# 这两个数就是在这两者之间取的——它是个**旋钮，不是判据**（`GLEAM_GO_FLAKE_ATTEMPTS` /
# `GLEAM_GO_FLAKE_BACKOFF` 可覆盖，探针就是靠它把等待压到 0 秒。但要注意：
# **探针慢的根因不是这里的等待，是进程创建本身**（本机实测约 2.7 秒/次）。
#
# **为什么必须打印**：静默重试等于把"闸门自己抖了一下"藏起来，
# 下一次它抖成别的样子时，没人会想到往这个方向查。
#
# **写法上的一个坑（改这里之前先读）**：调用方里有 `set -e` 的脚本（smoke-replay.sh）。
# `out=$(cmd)` 这种**裸赋值**在 `set -e` 下会直接退出脚本——重试逻辑一次都跑不到，
# 而症状是"脚本在第 48 行静默结束"，看起来像构建失败。
# 所以取退出码一律写成 `out=$(...) && rc=0 || rc=$?`：`&&`/`||` 列表里 `set -e` 不生效。
go_retry() {
  local name="$1"
  shift
  local out rc attempt=0
  while :; do
    out=$("$@" 2>&1) && rc=0 || rc=$?
    attempt=$((attempt + 1))

    # 成功：如果之前重试过，说一句——但别把它当成"正常"。
    if [ "$rc" -eq 0 ]; then
      if [ "$attempt" -gt 1 ]; then
        echo "  （第 $attempt 次尝试通过；若这条经常出现，说明该环境需要修，而不是继续重试）" >&2
      fi
      break
    fi

    # 真实失败是确定性的，且不会带这个签名 → 一次都不重试。
    if ! printf '%s' "$out" | grep -qE "$GO_FLAKE_RE"; then
      break
    fi

    # 是噪声，但尝试次数已用完 → 如实失败（环境问题不该被重试拖过去）。
    if [ "$attempt" -ge "$GO_FLAKE_ATTEMPTS" ]; then
      echo "  ⚠ Go 工具链环境噪声（$name）：试了 $attempt 次仍失败——这是环境问题，不是代码问题（见 docs/known-limits.md）：" >&2
      printf '%s\n' "$out" | grep -E "$GO_FLAKE_RE" | head -3 | sed 's/^/      /' >&2 || true
      break
    fi

    echo "  ⚠ Go 工具链环境噪声（$name）：第 $attempt 次尝试失败，等 ${GO_FLAKE_BACKOFF}s 再试（第 $attempt/$((GO_FLAKE_ATTEMPTS - 1)) 次重试）——不是代码问题：" >&2
    printf '%s\n' "$out" | grep -E "$GO_FLAKE_RE" | head -3 | sed 's/^/      /' >&2 || true
    sleep "$GO_FLAKE_BACKOFF"
  done

  if [ "$rc" -ne 0 ]; then
    printf '%s\n' "$out" | tail -25 | sed 's/^/    /' >&2
  fi
  return "$rc"
}
