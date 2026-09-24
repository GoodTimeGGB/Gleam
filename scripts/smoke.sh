#!/usr/bin/env bash
# Gleam 冒烟自测：驱动发布二进制走一遍完整 JSON-RPC 目标模式会话。
#
# **这个脚本必须真的会失败。** 下面两条注释都有来历，别再改回去：
#
#   ① `set -e` 对 `A && B` 形式的失败**不生效**——bash 只对 `&&`/`||` 列表里
#      「最后一个 && 之后的那条」生效。所以 `curl … && echo "✓"` 在检查失败时
#      既不报错也不退出，脚本照样打印「全部通过」。**闸门永远不会响**，比没有闸门更坏：
#      它给出的是"已验证"的错觉。
#   ② `grep -o … | head -1` 恒为 0（head 总是成功），这种写法等于没写检查。
#
# 因此：所有检查走 ok/bad 计数，结尾按失败数决定退出码；不用 `set -e`，
# 让"哪一条失败了"由计数说清，而不是让 shell 悄悄退出。
#
# 临时目录**不清理**，两个原因：
#   ① 本机的删除操作会经过一层安全删除策略，rm -rf 可能失败或直接挂住（表现为莫名的
#      SIGTERM）；清理失败还会把脚本的**退出码污染成 1**——业务全过、退出码为 1。
#      （这里原先写着「mktemp 在本机返回 Windows 形态路径 C:\…\Temp/tmp.X」，
#      2026-09-23 复核发现**这条已经不成立**：同一个 shell 里 `mktemp -d` 稳定返回
#      `/tmp/tmp.X`。返回形态取决于挂载与 TMPDIR，两种都出现过——所以真正的教训不是
#      "它返回哪种"，而是**任何路径都不能依赖 mktemp 的返回形态**：交给二进制的路径
#      一律过一次 cygpath，见下面的 `*C` 变量。这一条没做到就是第 5 层整层挂掉。）
#   ② 出问题时现场还在，可以直接翻。
set -uo pipefail
cd "$(dirname "$0")/.."

BIN=bin/gleam.exe
TMP=$(mktemp -d)

# Go 工具链环境噪声（`package X is not in std (…)`）的统一处理。
# 抽在 scripts/lib/ 里共用：verify.sh / smoke.sh / smoke-replay.sh 都会调 go build，
# 判据只写一份，免得只在一处加重试、另两处继续偶发变红。
# shellcheck source=lib/gonoise.sh
source scripts/lib/gonoise.sh

PASS=0
FAIL=0
ok() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
bad() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); }

echo "[smoke] 临时目录 $TMP（不清理）"
echo "[smoke] 构建二进制…"
go_retry "smoke 构建" go build -trimpath -ldflags="-s -w" -o "$BIN" ./cmd/gleam || {
  echo "[smoke] 构建失败，冒烟无法进行"
  exit 1
}

# Mock 响应脚本：规划 → 反思
cat > "$TMP/script.json" <<'EOF'
[
  {"kind":"plan","texts":["{\"steps\":[{\"id\":\"s1\",\"description\":\"写入问候\",\"tool\":\"file.write\",\"args\":{\"path\":\"smoke.txt\",\"content\":\"Hello Gleam\"}},{\"id\":\"s2\",\"description\":\"读取验证\",\"tool\":\"file.read\",\"args\":{\"path\":\"smoke.txt\"},\"depends_on\":[\"s1\"]},{\"id\":\"s3\",\"description\":\"回复\",\"tool\":\"reply\",\"args\":{\"text\":\"冒烟测试通过：smoke.txt 已创建\"}}],\"estimated_time\":\"short\"}"]},
  {"kind":"reflect","texts":["{\"score\":97,\"verdict\":\"done\",\"reason\":\"全部成功\",\"suggestion\":\"可以把该流程固化为技能\"}"]}
]
EOF

WS="$TMP/ws"; DATA="$TMP/data"; mkdir -p "$WS" || exit 1

# 交给**二进制**（Windows 程序）与 HTTP 的路径，一律转成 cygpath 形态。
#
# 两个理由，指向同一件事——**bash 眼里的路径和 Windows 程序眼里的路径不是一套**：
#   * 二进制：Windows 拿到 POSIX 形态的 `/tmp/tmp.X` 会解析成 `<当前盘>:\tmp\tmp.X`，
#     报 "The system cannot find the file specified"，而且是在**进程启动前**就失败。
#     后果不成比例：整个第 5 层以 0 通过收场，看起来像"产品全坏了"，其实只是路径形态不对。
#   * HTTP/JSON：JSON 字符串里的反斜杠是转义符，Windows 形态直接拼进去会变成非法转义
#     （\U \A \L …），接口返 400。以前这条失败被 `set -e` 对 `&&` 列表的豁免吞掉了，
#     而紧接着的"已持久化"检查因为启动参数写过同一个路径**蒙对了**——两处都是假象。
#
# 命名约定：**`*C` 结尾 = 已转换（converted），只给二进制/HTTP 用**；
# 不带后缀的（`$WS` / `$DATA` / `$TMP`）留给 shell 自己用（`[ -f "$WS/smoke.txt" ]`）。
# 混用这两种是这类脚本最常见的错，所以名字上就分开。
slash() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
TMPC=$(slash "$TMP")
WSC=$(slash "$WS")
DATAC=$(slash "$DATA")
# 切到一个**新**目录，这样"已持久化"才是真的在证明"切换生效了"，
# 而不是在证明"启动参数写过这个路径"。
WS2="$TMP/ws2"; mkdir -p "$WS2" || exit 1
WS2C=$(slash "$WS2")

OUT="$TMP/session.out"
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  sleep 0.5
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"goal/submit","params":{"goal":"创建 smoke.txt 并写入 Hello Gleam"}}'
  sleep 2
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"memory/save","params":{"content":"冒烟测试记忆条目","tags":["test"]}}'
  sleep 0.3
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"memory/search","params":{"query":"冒烟测试","k":3}}'
  sleep 0.3
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"skills/save","params":{"name":"smoke-skill","description":"冒烟技能","steps":[{"id":"s1","tool":"file.write","args":{"path":"sk.txt","content":"技能输出"}}]}}'
  sleep 0.3
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"skills/run","params":{"name":"smoke-skill"}}'
  sleep 2
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"schedule/create","params":{"name":"smoke-job","goal":"定时冒烟","interval_sec":3600}}'
  sleep 0.3
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"schedule/list","params":{}}'
  sleep 0.3
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"shutdown","params":{}}'
  sleep 1
} | GLEAM_NO_SYS_NOTIFY=1 "$BIN" serve --mock-llm --mock-script "$TMPC/script.json" --workspace "$WSC" --data-dir "$DATAC" > "$OUT" 2>"$TMP/err.log"

echo "===== 会话输出（关键行） ====="
grep -q '"method":"goal/progress"' "$OUT" && ok "goal/progress 推送" || bad "goal/progress 推送"
grep -q '"method":"goal/completed"' "$OUT" && ok "goal/completed 推送" || bad "goal/completed 推送"
grep -q '"status":"success"' "$OUT" && ok "任务成功" || bad "任务成功"
grep -q '"suggestion"' "$OUT" && ok "主动提议推送" || bad "主动提议推送"
grep -q '"id":3' "$OUT" && ok "memory/save 应答" || bad "memory/save 应答"
grep -q '冒烟测试记忆条目' "$OUT" && ok "memory/search 命中" || bad "memory/search 命中"
grep -q '"version":1' "$OUT" && ok "skills/save 版本化" || bad "skills/save 版本化"
grep -q '"status":"success"' "$OUT" && ok "skills/run 执行成功" || bad "skills/run 执行成功"
grep -q 'smoke-job' "$OUT" && ok "schedule/create + list" || bad "schedule/create + list"
[ -f "$WS/smoke.txt" ] && [ "$(cat "$WS/smoke.txt")" = "Hello Gleam" ] && ok "目标产物 smoke.txt 内容正确" || bad "目标产物 smoke.txt 内容正确"
[ -f "$WS/sk.txt" ] && [ "$(cat "$WS/sk.txt")" = "技能输出" ] && ok "技能产物 sk.txt 正确" || bad "技能产物 sk.txt 正确"
[ -d "$DATA/tasks" ] && ok "工作记忆已持久化 tasks/" || bad "工作记忆已持久化 tasks/"
[ -f "$DATA/schedules.json" ] && ok "调度任务已持久化 schedules.json" || bad "调度任务已持久化 schedules.json"
[ -f "$DATA/memory/longterm.json" ] && ok "长期记忆已持久化 longterm.json" || bad "长期记忆已持久化 longterm.json"

# ---------- Web UI 冒烟 ----------
PORT=8791
# GLEAM_NO_SYS_NOTIFY=1：冒烟会真的触发定时任务，不禁用就会在开发机上弹系统通知。
# 禁用**不是**静默丢弃——待发内容会打到 stderr（webui.out），所以"通知到底发了没有"
# 仍然可断言。这条断言正是本批 P0 的端到端出口：判据对但线没接，本仓库栽过四次。
GLEAM_NO_SYS_NOTIFY=1 "$BIN" webui --addr "127.0.0.1:$PORT" --mock-llm --mock-script "$TMPC/script.json" --workspace "$WSC" --data-dir "$DATAC" > "$TMP/webui.out" 2>&1 &
WEBPID=$!
trap 'kill "${WEBPID:-}" 2>/dev/null || true' EXIT
sleep 1.5

curl -sf "http://127.0.0.1:$PORT/api/info" | grep -q '"name":"gleam"' && ok "WebUI /api/info" || bad "WebUI /api/info"
curl -sf "http://127.0.0.1:$PORT/" | grep -q "Gleam" && ok "WebUI 首页渲染" || bad "WebUI 首页渲染"
curl -sf -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PORT/assets/app.js" | grep -q 200 && ok "WebUI 静态资源" || bad "WebUI 静态资源"
curl -sf -X POST "http://127.0.0.1:$PORT/api/heartbeat" -o /dev/null -w "%{http_code}" | grep -q 200 && ok "WebUI 心跳 /api/heartbeat" || bad "WebUI 心跳 /api/heartbeat"
curl -sf -X POST "http://127.0.0.1:$PORT/api/hooks/smoke-job" | grep -q '"triggered":true' && ok "WebUI HTTP 回调触发 /api/hooks/smoke-job" || bad "WebUI HTTP 回调触发 /api/hooks/smoke-job"

# ---------- 定时任务的通知策略（本批 P0：结果必须送达） ----------
# 这几条不只验"接口通"，而是验**接线通**：策略存得下、读得回、非法值被拒，
# 最后真的触发一次任务，看通知路径有没有跑到。
curl -sf -X POST "http://127.0.0.1:$PORT/api/schedules" -H "Content-Type: application/json" \
  -d '{"name":"notify-job","goal":"定时冒烟","interval_sec":3600,"notify":"always"}' \
  | grep -q '"notify":"always"' && ok "定时任务可带通知策略创建" || bad "定时任务可带通知策略创建"
curl -sf "http://127.0.0.1:$PORT/api/schedules" | grep -q '"name":"notify-job"' \
  && ok "定时任务出现在列表" || bad "定时任务出现在列表"
curl -sf -X POST "http://127.0.0.1:$PORT/api/schedules/notify-job/notify" -H "Content-Type: application/json" \
  -d '{"notify":"never"}' | grep -q '"notify":"never"' && ok "通知策略可改（always → never）" || bad "通知策略可改（always → never）"
curl -sf -X POST "http://127.0.0.1:$PORT/api/schedules/notify-job/notify" -H "Content-Type: application/json" \
  -d '{"notify":""}' | grep -q '"notify":"on_failure"' && ok "空串恢复默认策略 on_failure" || bad "空串恢复默认策略 on_failure"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:$PORT/api/schedules/notify-job/notify" \
  -H "Content-Type: application/json" -d '{"notify":"yelling"}')
[ "$CODE" = "400" ] && ok "非法通知策略被拒（400）" || bad "非法通知策略被拒（400，实际 $CODE）"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:$PORT/api/schedules/no-such-job/notify" \
  -H "Content-Type: application/json" -d '{"notify":"never"}')
[ "$CODE" = "400" ] && ok "改不存在的任务被拒（400）" || bad "改不存在的任务被拒（400，实际 $CODE）"

# 端到端送达：设成 always 后触发一次，通知路径必须真的跑到。
# 这条是本批的核心断言——只验策略表格不够，本仓库栽过四次的正是"判据对、线没接"。
curl -sf -X POST "http://127.0.0.1:$PORT/api/schedules/notify-job/notify" -H "Content-Type: application/json" \
  -d '{"notify":"always"}' >/dev/null
curl -sf -X POST "http://127.0.0.1:$PORT/api/hooks/notify-job" >/dev/null
sleep 3
grep -q "系统通知（已禁用，仅打印）" "$TMP/webui.out" && ok "定时任务结果送达（通知路径真的跑了）" || bad "定时任务结果送达（通知路径真的跑了）"
grep -q "定时任务「notify-job」" "$TMP/webui.out" && ok "通知里点明了是哪个任务" || bad "通知里点明了是哪个任务"
curl -sf "http://127.0.0.1:$PORT/api/settings" | grep -q '"persona"' && ok "设置读取 /api/settings" || bad "设置读取 /api/settings"
curl -sf -X POST "http://127.0.0.1:$PORT/api/settings" -H "Content-Type: application/json" -d '{"persona":{"style":"gentle"},"agent":{"step_retries":2}}' | grep -q '"style":"gentle"' && ok "设置保存并热生效" || bad "设置保存并热生效"
grep -q "style: gentle" "$DATA/settings.yaml" && ok "设置覆盖层已持久化 settings.yaml" || bad "设置覆盖层已持久化 settings.yaml"
curl -sf "http://127.0.0.1:$PORT/api/context" | grep -q '"short_turns"' && ok "上下文状态 /api/context" || bad "上下文状态 /api/context"
curl -sf "http://127.0.0.1:$PORT/" | grep -q "view-settings" && ok "设置视图已内嵌首页" || bad "设置视图已内嵌首页"
curl -sf -X POST "http://127.0.0.1:$PORT/api/workspace" -H "Content-Type: application/json" -d "{\"path\":\"$WS2C\"}" | grep -q '"workspace"' && ok "工作区切换 /api/workspace" || bad "工作区切换 /api/workspace"
grep -q "ws2" "$DATA/settings.yaml" && ok "工作区切换已持久化（切到新目录 ws2）" || bad "工作区切换已持久化（切到新目录 ws2）"
curl -sf "http://127.0.0.1:$PORT/api/fs?path=$WSC" | grep -q '"dirs"' && ok "文件夹浏览 /api/fs" || bad "文件夹浏览 /api/fs"
curl -sf "http://127.0.0.1:$PORT/api/providers" | grep -q '"zhipu"' && ok "厂商预设 /api/providers（coding/agent/token 入口）" || bad "厂商预设 /api/providers（coding/agent/token 入口）"
curl -sf "http://127.0.0.1:$PORT/api/market/mcp" | grep -q '"filesystem"' && ok "MCP 市场目录" || bad "MCP 市场目录"
curl -sf -X POST "http://127.0.0.1:$PORT/api/mcp" -H "Content-Type: application/json" -d '{"name":"smoke-mcp","command":"no-such-mcp-cmd","args":[]}' | grep -q '"installed":true' && ok "MCP 自定义安装（连接失败仅告警）" || bad "MCP 自定义安装（连接失败仅告警）"
grep -q "name: smoke-mcp" "$DATA/settings.yaml" && ok "MCP 配置已持久化到覆盖层" || bad "MCP 配置已持久化到覆盖层"
curl -sf -X DELETE "http://127.0.0.1:$PORT/api/mcp/smoke-mcp" | grep -q '"name":"smoke-mcp"' && ok "MCP 卸载" || bad "MCP 卸载"
curl -sf "http://127.0.0.1:$PORT/api/market/skills" | grep -q 'quick-note' && ok "技能市场目录" || bad "技能市场目录"
curl -sf -X POST "http://127.0.0.1:$PORT/api/market/skills/install" -H "Content-Type: application/json" -d '{"name":"quick-note"}' | grep -q '"installed":true' && ok "技能市场一键安装" || bad "技能市场一键安装"
curl -sf "http://127.0.0.1:$PORT/" | grep -q "view-market" && ok "市场视图已内嵌首页" || bad "市场视图已内嵌首页"
TASK=$(curl -sf -X POST "http://127.0.0.1:$PORT/api/goals" -H "Content-Type: application/json" -d '{"goal":"创建 smoke.txt 并写入 Hello Gleam","mode":"auto"}' | grep -o '"task_id":"[a-f0-9]*"' | cut -d'"' -f4)
[ -n "$TASK" ] && ok "WebUI 目标提交 task_id=$TASK" || bad "WebUI 目标提交"
ST=""
for i in $(seq 1 40); do
  ST=$(curl -sf "http://127.0.0.1:$PORT/api/goals/$TASK" | grep -o '"status":"[a-z]*"' | head -1 | cut -d'"' -f4)
  [ "$ST" != "running" ] && break
  sleep 0.25
done
[ "$ST" = "success" ] && ok "WebUI 目标执行成功" || bad "WebUI 目标执行成功（status=$ST）"

kill "${WEBPID:-}" 2>/dev/null || true

echo ""
if [ "$FAIL" -gt 0 ]; then
  echo "===== 冒烟测试未通过：$PASS 通过 / $FAIL 失败 ====="
  echo "现场保留在 $TMP"
  echo "--- session.out 尾部 ---"
  tail -20 "$OUT" 2>/dev/null || true
  echo "--- webui.out 尾部 ---"
  tail -20 "$TMP/webui.out" 2>/dev/null || true
  exit 1
fi
echo "===== 冒烟测试全部通过（$PASS 项） ====="
