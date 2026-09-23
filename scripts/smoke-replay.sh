#!/usr/bin/env bash
# 审计四用法的端到端冒烟：回放 / 重跑 / 恢复 / 分叉 / 比较。
#
# 为什么不靠单测：单测能证明"函数返回了什么"，证明不了"这条命令真的只跑了第三步"。
# 这里用一个**三步任务在第 3 步失败**的记录，验证 `--from s3` 之后：
#   前两步标为沿用、且它们要写的文件**没有被再次创建**（工具真的没被再调用）。
# 副作用是否发生只能从文件系统上看，所以这一层必须走真实二进制。
#
# 断言顺序有意为之：**先证明命令真的跑起来了、第三步真的执行了，再证明前两步没被重做**。
# 反过来写，"文件没被创建"会在命令压根没跑起来时也成立——用错误的理由通过，
# 比直接失败更坏。
set -euo pipefail
cd "$(dirname "$0")/.."

# Go 工具链环境噪声（`package X is not in std (…)`）的统一处理，判据见 scripts/lib/gonoise.sh。
# 本脚本带 `set -e`，而 go_retry 内部已按 set -e 安全的写法取退出码——别在这里改成裸赋值。
# shellcheck source=lib/gonoise.sh
source scripts/lib/gonoise.sh

BIN=bin/gleam-replay-smoke.exe
# 临时目录用系统临时目录，**而且不清理**。
#
# 两个原因，都是被环境教出来的：
#   ① 本机的删除操作会经过一层安全删除策略，rm -rf / rmdir / find -delete 都可能
#      被挂起等待确认（在脚本里就是直接卡死，表现为莫名的 SIGTERM）。与其写一个
#      "有时会挂住"的清理，不如交给操作系统回收——每次 mktemp 都是新目录，
#      重复运行天然是干净的。
#   ② 出问题时现场还在，可以直接翻。
#
# **路径必须转成 Windows 形态再传给二进制**：Git Bash 的 /d/... 到了 Go 程序里会
# 变成 \d\... 而读不到文件。同时任务记录里要写**绝对**路径——cygpath 对相对路径
# 不会补全，于是"绝对路径"其实是相对的，会被工具再按工作区解析一次（路径被拼两遍
# ws/ws/c.txt），而症状看起来像"文件不存在"，很容易被误判成权限或工具问题。
TMP=$(mktemp -d)

WS="$TMP/ws"
DATA="$TMP/data"
# tasks/ 与 runs/ 必须预先建好：脚本用重定向往里写记录，目录不存在会直接失败。
mkdir -p "$WS" "$DATA/tasks" "$DATA/runs"

# 两种路径形态各有各的用处，不能混：
#   * bash 侧（重定向、mkdir）用 $TMP / $WS / $DATA
#   * 传给 Windows 二进制的（--config/--data-dir）用 $TMPC / $DATAC
if command -v cygpath >/dev/null 2>&1; then
  WSC=$(cygpath -m "$WS")
  DATAC=$(cygpath -m "$DATA")
  TMPC=$(cygpath -m "$TMP")
else
  WSC="$WS"; DATAC="$DATA"; TMPC="$TMP"
fi

echo "[smoke-replay] 构建二进制…"
go_retry "smoke-replay 构建" go build -o "$BIN" ./cmd/gleam

cat > "$TMP/config.yaml" <<EOF
llm:
  provider: mock

safety:
  mode: auto

workspace: "$WSC"
data_dir: "$DATAC"
EOF

# ---------- 造一条"三步任务在第 3 步失败"的记录 ----------
#
# s1/s2 是 file.write：它们的副作用（文件出现）就是"工具有没有被再调用"的证据。
# s3 是 file.read 一个当时不存在的文件——这是**可恢复**的失败，把文件补上就能成功，
# 于是重放能演示"同一前提、只改了环境，结果从失败变成功"。
cat > "$DATA/tasks/t-smoke.json" <<EOF
{
 "task_id": "t-smoke",
 "goal": "三步任务：写两个文件，读第三个",
 "status": "failed",
 "executed_plan": {"steps": [
   {"id": "s1", "tool": "file.write", "args": {"path": "$WSC/a.txt", "content": "A"}},
   {"id": "s2", "tool": "file.write", "args": {"path": "$WSC/b.txt", "content": "B"}},
   {"id": "s3", "tool": "file.read", "args": {"path": "$WSC/c.txt"}}
 ]},
 "steps": [
   {"step_id": "s1", "tool": "file.write", "status": "succeeded", "outcome": "ok", "duration_ms": 1},
   {"step_id": "s2", "tool": "file.write", "status": "succeeded", "outcome": "ok", "duration_ms": 1},
   {"step_id": "s3", "tool": "file.read", "status": "failed", "outcome": "failed", "error": "文件不存在", "duration_ms": 1}
 ],
 "config_snapshot": {"provider": "mock", "model": "mock", "max_concurrency": 2, "step_timeout_secs": 30, "dedupe_calls": true, "max_output_runes": 6000}
}
EOF

# 把当时缺失的文件补上：这是**唯一**被改动的变量。
printf 'C' > "$WS/c.txt"

echo "[smoke-replay] 恢复：--from s3（前两步应沿用，不重新执行）…"
set +e
OUT=$("$BIN" replay t-smoke --config "$TMPC/config.yaml" --data-dir "$DATAC" --from s3 2>&1)
set -e
echo "$OUT" | sed 's/^/    /'

fail=0
check() { if eval "$2"; then echo "  ✓ $1"; else echo "  ✗ $1"; fail=1; fi; }

echo "===== 断言 ====="
# 0) 命令本身必须跑起来。命令没跑起来时，"文件没被创建"照样成立——
#    那种"以错误的理由通过"比直接失败更坏，所以先把它挡住。
check "恢复命令本身跑起来了（有对比输出）" "echo \"\$OUT\" | grep -q '对比（'"

# 1) 第三步**真的执行了**：文件补上之后 s3 从失败变成功。
#    只 grep 步骤名是没用的（它本来就会出现），必须看结果真的变了——
#    否则"沿用被误当成执行"这种情况会被漏过去。
check "s3 真的执行了（结果从失败变成功）" "echo \"\$OUT\" | grep -q 's3 (file.read)：变化'"
check "s3 这次成功" "echo \"\$OUT\" | grep -q '成功 3'"

# 2) 沿用必须显式标注——否则"这次成功 3 步"会被读成"这次跑了 3 步"
check "输出标出沿用（未执行）" "echo \"\$OUT\" | grep -q '沿用'"
check "输出标出沿用步数" "echo \"\$OUT\" | grep -q '2 步沿用'"
check "沿用的两步没有被标成变化" "echo \"\$OUT\" | grep -q 's1 (file.write)：一致 \[沿用\]'"

# 3) 只执行第三步：前两步的副作用**没有**再次发生
check "s1 被沿用，a.txt 没有被再次写入" "[ ! -f '$WS/a.txt' ]"
check "s2 被沿用，b.txt 没有被再次写入" "[ ! -f '$WS/b.txt' ]"

# 4) 参数来源要说清（有快照 → 取自快照）
check "说明参数取自任务启动时的快照" "echo \"\$OUT\" | grep -q '参数取自任务启动时的快照'"
check "印出配置快照" "echo \"\$OUT\" | grep -q '配置快照：'"

# 5) 回放产物落 replays/，**不进 tasks/**（否则污染质量统计的分母）
check "回放记录落 replays/" "ls '$DATA/replays'/*.json >/dev/null 2>&1"
check "tasks/ 里仍然只有那一条任务" "[ \$(ls '$DATA/tasks' | wc -l) -eq 1 ]"
# 6) 正常结束后运行日志被删掉（runs/ 只留没跑完的运行）
check "运行日志已清理（runs/ 里没有残留）" "[ ! -f '$DATA/runs/replay-t-smoke.jsonl' ]"

# ---------- 回放记录自身：起点与沿用要写进记录里 ----------
#
# 重放时印的是"与原任务的对比"，起点信息不在那一屏里；它写在**记录**里。
# 事后翻记录的人要能一眼看出"这次是从哪一步起的、前面几步是沿用的"——
# 不测这条路径，记录里少印一句也没人会发现。
REPLAY_ID=$(ls "$DATA/replays" | head -1 | sed 's/\.json$//')
echo "[smoke-replay] 翻记录：replay $REPLAY_ID"
REC=$("$BIN" replay "$REPLAY_ID" --config "$TMPC/config.yaml" --data-dir "$DATAC" 2>&1 || true)
echo "$REC" | sed 's/^/    /'
check "记录里写明起点" "echo \"\$REC\" | grep -q '起点：s3 起'"
check "记录里说明之前的步骤是沿用、没有重新执行" "echo \"\$REC\" | grep -q '之前的步骤沿用当时结果，没有重新执行'"
check "记录里写明这次回放的类型是恢复" "echo \"\$REC\" | grep -q '（恢复）'"
check "记录里印出沿用步数" "echo \"\$REC\" | grep -q '2 步沿用'"

# ---------- 比较：两条记录逐步对比 ----------
echo "[smoke-replay] 比较：--diff t-smoke $REPLAY_ID"
DIFF=$("$BIN" replay --diff "t-smoke" "$REPLAY_ID" --config "$TMPC/config.yaml" --data-dir "$DATAC" 2>&1)
echo "$DIFF" | sed 's/^/    /'
check "对比印出沿用步数" "echo \"\$DIFF\" | grep -q '沿用'"
check "对比标出逐步差异（s3 从失败变成功）" "echo \"\$DIFF\" | grep -q 's3 (file.read)：变化'"
check "对比标出沿用的那一步" "echo \"\$DIFF\" | grep -q '\[沿用\]'"

# ---------- 分叉：同一前提换一种走法 ----------
#
# 分叉与恢复共用"从某一步起"的机制，区别只在**那一步换了走法**。
# 换掉 s3 的工具（读文件 → 列目录），前两步照旧沿用：同一前提、只动走法，
# 于是"这次差异是不是换工具带来的"这个问题才有唯一答案。
echo "[smoke-replay] 分叉：--from s3 --tool file.list"
FORK=$("$BIN" replay t-smoke --config "$TMPC/config.yaml" --data-dir "$DATAC" \
  --from s3 --tool file.list --args "{\"path\":\"$WSC\"}" 2>&1)
echo "$FORK" | sed 's/^/    /'
check "分叉命令跑起来了" "echo \"\$FORK\" | grep -q '分叉中'"
check "分叉说明改了什么" "echo \"\$FORK\" | grep -q 'file.list'"
check "分叉后 s3 换了工具" "echo \"\$FORK\" | grep -q '换工具（file.read → file.list）'"
# 注意措辞：这一屏结尾是"对比"渲染，沿用数写成"沿用：0 步 → 2 步"；
# "N 步沿用"那种写法只在记录渲染里出现。断言抄错措辞会变成假失败。
check "分叉仍然沿用前两步" "echo \"\$FORK\" | grep -q '沿用：  0 步 → 2 步'"
# 分叉产物也是回放记录，同样落 replays/，不会多出一条"任务"
FORK_ID=$(ls "$DATA/replays" | grep -v "$REPLAY_ID" | head -1 | sed 's/\.json$//')
check "分叉也落进 replays/（tasks/ 仍只有一条）" "[ \$(ls '$DATA/tasks' | wc -l) -eq 1 ]"
FORKREC=$("$BIN" replay "$FORK_ID" --config "$TMPC/config.yaml" --data-dir "$DATAC" 2>&1 || true)
check "分叉记录写明类型是分叉" "echo \"\$FORKREC\" | grep -q '（分叉）'"
check "分叉记录写明改动" "echo \"\$FORKREC\" | grep -q '工具=file.list'"

# ---------- 缺记录就拒绝：不能把恢复悄悄变成重跑 ----------
echo "[smoke-replay] 反例：起点之前的步骤没有结果记录时必须拒绝…"
cat > "$DATA/tasks/t-gap.json" <<EOF
{
 "task_id": "t-gap",
 "goal": "缺记录的恢复",
 "status": "failed",
 "executed_plan": {"steps": [
   {"id": "s1", "tool": "file.write", "args": {"path": "$WSC/x.txt", "content": "X"}},
   {"id": "s2", "tool": "file.write", "args": {"path": "$WSC/y.txt", "content": "Y"}}
 ]},
 "steps": [
   {"step_id": "s2", "tool": "file.write", "status": "failed", "outcome": "failed", "error": "当时没做成"}
 ]
}
EOF
set +e
GAP=$("$BIN" replay t-gap --config "$TMPC/config.yaml" --data-dir "$DATAC" --from s2 2>&1)
set -e
echo "$GAP" | sed 's/^/    /'
check "缺记录时拒绝恢复并说明原因" "echo \"\$GAP\" | grep -q '没有结果记录'"
check "拒绝时指出副作用风险" "echo \"\$GAP\" | grep -q '副作用'"
check "拒绝时给出出路（换起点或 --rerun）" "echo \"\$GAP\" | grep -q 'rerun'"
# 关键：拒绝之后 x.txt 也没有被创建——拒绝是**真的拒绝**，不是"先跑一下再说"
check "拒绝之后没有产生任何副作用" "[ ! -f '$WS/x.txt' ] && [ ! -f '$WS/y.txt' ]"

# ---------- 运行中退出：运行日志是唯一凭据 ----------
echo "[smoke-replay] 只跑了半截的运行（只有运行日志）…"
printf '%s\n' '{"step_id":"s1","tool":"file.write","status":"succeeded","outcome":"ok","duration_ms":1}' > "$DATA/runs/t-half.jsonl"
set +e
HALF=$("$BIN" replay t-half --config "$TMPC/config.yaml" --data-dir "$DATAC" 2>&1)
set -e
echo "$HALF" | sed 's/^/    /'
check "半截运行能从运行日志里读出来" "echo \"\$HALF\" | grep -q '没有跑完'"
check "半截运行无法重跑（没有落盘计划）" "echo \"\$HALF\" | grep -q '无法重跑'"

if [ "$fail" -ne 0 ]; then
  echo "===== 冒烟失败（现场：$TMP） ====="
  exit 1
fi
echo "===== 冒烟全部通过 ====="
