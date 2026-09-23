---
name: gleam-release-verify
description: Gleam 项目的「改代码 → 跑测试 → 更文档 → 打包 → 真机冒烟」发布闭环。当需要给 Gleam 加/改特性后做全量验证与出包，或排查 e2e 构建超时、内嵌前端不生效、冒烟脚本起不来等问题时使用。关键词：Gleam 打包、package.sh、e2e 超时、内嵌前端、真机冒烟、验收标准、循环治理。
agent_created: true
---

# Gleam 发布验证闭环

Gleam 是 Go 1.22+、零第三方依赖的本地优先桌面 Agent（`D:\PersonalProject\Gleam`）。
这个 skill 记录「改完代码到出包冒烟」的固定套路，以及几个反复踩到的坑。

## 一、全量回归：e2e 要单独跑，且必须给它热 GOCACHE

**不要**直接 `go test ./internal/... ./pkg/...`。`internal/e2e` 要现场构建真二进制，
和全量包并行时抢不到资源，会报一串 `构建超时: context deadline exceeded`（假失败）。

**更关键的一点**：`internal/e2e/e2e_test.go` 的 `buildBinary` 在 `GOCACHE` 未设置时，
会用一个**临时空缓存**（`<tmp>/gocache`）来构建，等于把整个标准库从零编一遍——
在这台机器上约 2 分钟，而它的构建超时正好是 2 分钟，于是**时好时坏**：
缓存热一点就过（94~128s），机器稍忙就必挂。

**正确做法：显式传入热缓存**，构建瞬间完成，稳定通过：

```bash
cd /d/PersonalProject/Gleam
GOCACHE="C:/Users/Administrator/AppData/Local/go-build" \
  go test ./internal/e2e/ -count=1 -timeout 600s -v
```

（`go env GOCACHE` 的值就是它；项目里那个 535M 的 `.gocache/` 是历史遗留，用不用都行。）

完整回归顺序：

```bash
# 1) 除 e2e 外的全量（约 1~4 分钟，视机器负载）
go list ./internal/... ./pkg/... | grep -v '/internal/e2e$' > .pkgs.txt
go test $(cat .pkgs.txt) -count=1 -timeout 400s; rm -f .pkgs.txt

# 2) e2e 单独跑，带热缓存（约 130s）
GOCACHE="C:/Users/Administrator/AppData/Local/go-build" \
  go test ./internal/e2e/ -count=1 -timeout 600s -v
```

判定标准：21 个包 `ok`，e2e 的 5 个用例（GoalCommand / ServeSession / MCPConnectorFullChain /
BinarySelfCheck / WebUI）全 PASS。

**本机 Go 工具链会偶发** `package xxx is not in std`（如 `package slices is not in std`、
`package log/internal is not in std`）。与代码无关，重试即过，别去改代码。

**全量测试后立刻接着跑 `go build ./...` 可能被 SIGTERM**（内存压力），
等几秒或改成分包构建（`go build ./internal/agent/`）即可；`go build ./... ; echo exit=$?`
重试一次通常就过。

## 二、打包前必须冻结 go:embed 的前端

`internal/webui/static/*`（app.js / style.css / index.html）是 `go:embed` 进二进制的。
`scripts/package.sh` 要跑 5 个平台约 6–9 分钟，**期间改动前端会导致各平台内嵌版本不一致**
（先构建的平台拿到旧版，后构建的拿到新版）。

流程：**先把 Go 与前端全部改完、`node --check` 通过，再启动打包，期间不要动任何被 embed 的文件。**

`node --check` 是前端改动的最低门槛（能拦住 `esc` 重复声明这类 SyntaxError）：

```bash
"C:/Users/Administrator/.workbuddy-ai/binaries/node/versions/22.22.2-2/node.exe" --check internal/webui/static/app.js
```

### 打包后必做：校验内嵌内容是最新的

embed 是原始字节，可直接 grep 二进制找新增的字符串：

```bash
cd /d/PersonalProject/Gleam
for f in dist/Gleam-*20260918*.exe dist/Gleam-Linux-x86_64-20260918 dist/Gleam-macOS-*20260918; do
  printf '%-52s ' "$(basename $f)"
  if grep -qa '未达标说明' "$f"; then echo "新前端 ✓"; else echo "旧前端 ✗"; fi
done
```

（把 `未达标说明` 换成本次新增的前端文案。）只要有 ✗，就重新跑一次 `bash scripts/package.sh`。

## 三、真机冒烟

```bash
# 启动（注意是 -addr 不是 -port；--mock-script 等路径参数必须用 Windows 风格 D:/... ）
./dist/Gleam-Windows-x86_64-20260918.exe webui --mock-llm \
  --mock-script "D:/PersonalProject/Gleam/.smoke-script.json" \
  --addr 127.0.0.1:18778 \
  --data-dir "D:/PersonalProject/Gleam/.smoke-data" \
  --workspace "D:/PersonalProject/Gleam/.smoke"
```

用 `run_in_background: true` 启动（`(cmd &)` 会随 Bash 调用结束被杀）。
等就绪再请求，**curl 必须加 `--noproxy '*'`**，否则本机 HTTP 代理返回 502：

```bash
for i in $(seq 1 15); do
  out=$(curl -s --noproxy '*' "http://127.0.0.1:18778/api/info"); [ -n "$out" ] && break; sleep 1
done
```

`--mock-script` 的格式是 `[{"kind":"plan","texts":["..."]}]`，**每个 kind 的 texts 是按轮次消费的队列**
（用尽后回退 `defaultReply`）。要验证多轮行为（如重规划后的状态校正），必须给每一轮都准备一条。

冒烟收尾：`TaskStop` 停掉后台任务，再删掉 `.smoke*`（被占用的句柄要等进程真正退出后才能删）。

### 要验「辅助模型」链路时，--mock-llm 不够用

`RebuildFastClient` 在 `Provider == "mock"` 时**刻意把 `FastLLM` 置空**，所以审核模型、
对话自检这类"只在配了 fast_model 时生效"的特性，用 `--mock-llm` 冒烟是**永远不触发**的
（会让你误以为功能坏了）。

正确做法：起一个本地假 OpenAI 兼容端点，用真配置指向它。要点：

- 假端点按**系统提示词里的任务标记**分流（`GLEAM-TASK:CHAT_CHECK` / `GLEAM-TASK:CHAT` / `GLEAM-TASK:PLAN`），
  就能一个端点同时扮演主模型和辅助模型
- 非流式响应形如 `{"choices":[{"message":{"content":"..."}}],"usage":{...}}`；
  流式要发 `data: {"choices":[{"delta":{"content":"..."}}]}` 再以 `data: [DONE]` 收尾
- 配置用 `provider: glm`（只有 `glm` 与 `mock` 两个值；`protocol` 才决定线协议）+ `base_url` 指向本地
- 端点端口别和 webui 端口撞，路径是 `/v1/chat/completions`

### 离线命令冒烟：`gleam rules` 不需要运行时

规则表是编译期常量，`gleam rules` 刻意**不装配运行时**（不读配置、不连模型、不碰数据目录），
所以在任何机器上都能跑，CI 里也能直接用：

```bash
./dist/xxx.exe rules                                    # 全表 + 版本指纹
./dist/xxx.exe rules --task-mode work                   # 应列出 code.* 为「未生效」
./dist/xxx.exe rules --task-mode chat                   # 生效 0 条，并解释"不经规划"
./dist/xxx.exe rules --task-mode code --tier coding --json
```

判据：改了 `internal/agent/rules.go` 之后，指纹**必须**变（`RuleSetVersion` 覆盖内容与范围）；
没改却变了，说明指纹漏算或算多了字段。

## 四、改测试时的两个坑

1. **共享 fixture 的桩不要每轮返回同一份 plan**：`internal/webui` 的 `stubLLM` 若固定返回同一
   planJSON，会触发**防打转检测**，任务被提前判 failed，把你要验的特性盖掉。
   验别的特性时先隔离：`f.agent.Cfg.Agent.StuckThreshold = 0`。
2. **`llm.Mock` 的 `reflectScript` 只覆盖 score/verdict/reason**。要验逐条验收判定，得自己造
   `llm.Scripted{Kind: "reflect", Texts: []string{原始 JSON}}`（见 `acceptance_test.go` 的 `reflectChecksScript`）。

## 五、加"辅助调用"类特性时的项目约定

新增一个由辅助模型（`fast_model`）承担的调用时，四条约定已成惯例，照抄即可：

1. **只在 `a.FastLLM != nil` 时启用**（审核模型 `reviewer()`、对话自检 `shouldChatCheck` 都这样）。
   理由：用主模型做辅助调用等于多花一份钱。设置页文案要注明"需要配置「辅助模型」才生效"。
2. **一律 fail-open**：调用失败、输出解析不了、无事可判，都保持原行为，绝不阻断主流程。
3. **同步加 `Marker*` 常量 + `KindOf` case**（如 `MarkerChatChk` / `chat_check`），否则 mock 脚本
   按错 kind 匹配、用量归因也会错。
4. **独立上下文**：只给它判定所需的最小输入，**不给它看生成时的推理**——生成者能给任何产出
   配上说得过去的理由。

配置项要同步改三处：`config.go`（字段 + `Default()` + `SaveOverlay` + `apply`）、
`webfacade.go`（`SettingsView` + patch 校验）、前端（`index.html` 控件 + `app.js` 回填与保存）。

## 六、文档同步清单

一次特性落地要同步四处，别漏：

| 文件 | 改什么 |
| --- | --- |
| `README.md` | 能力表加一行（`| **名称** | 一句话说清"做了什么、为什么这么做" |`） |
| `Gleam 技术设计文档.md` | 新增 `### 4.6.x` 小节，写问题 → 做法 → 取舍 |
| `pack/CHANGELOG.md` | 在**最上面的日期段**追加，含测试用例数与验证结论 |
| `website/index.html` | 对应分组（成本治理 / 循环治理与安全 / 创作与成长）补卡片 |

写中文文档时注意：`✓` `✗` 这类符号在部分写入路径下会变成 `?`，写完 `grep -n '?' <file>` 核一下。

### 编码坑：pack/CHANGELOG.md 是 GBK

仓库里**只有 `pack/CHANGELOG.md` 是 GBK**，其余（README、设计文档、官网、Go 源码、前端）都是 UTF-8。

后果：用 Python 脚本改它时会 `UnicodeDecodeError: 'utf-8' codec can't decode byte 0xd3`，
`grep` 也会把它当二进制文件（输出 `Binary file ... matches`）。

处理办法（二选一）：
- **首选**：用 Edit/Write 工具改它——工具会按原编码回写，GBK 不会被破坏（已验证）
- 非要用脚本：读用 `encoding='gbk'`；写**必须先编码校验、再原子替换**，见下

#### ⚠ 写回 GBK 文件：绝不要直接 `open(path,'w',encoding='gbk')`

**这条是真踩过的**：`open(path,"w")` 会**立刻清空原文件**，而编码错误发生在写入阶段。
于是 `UnicodeEncodeError` 一抛，文件就变成 **0 字节**——原始内容没了。
（实际事故：往 CHANGELOG 插一节时用了 Unicode 减号 `−`（U+2212），GBK 编不了，
`pack/CHANGELOG.md` 当场被清空。）

正确写法——先编码，编不了就别碰原文件；编好了先写临时文件再替换：

```python
out = ...  # 拼好的新内容
try:
    encoded = out.encode("gbk")          # ① 先校验目标编码
except UnicodeEncodeError as e:
    print("GBK 编不了 %r（位置 %d）" % (out[e.start:e.end], e.start))
    raise SystemExit(1)
tmp = path + ".tmp"
open(tmp, "wb").write(encoded)           # ② 先写临时文件
os.replace(tmp, path)                    # ③ 原子替换
```

配套两条：
- **读进来先判空**：`raw = open(path,'rb').read()`，`if not raw: 拒绝继续`。空文件说明上一次就写坏了。
- **GBK 编不了的常见字符**：`−`(U+2212 减号，用 ASCII `-`)、`✓`/`✗`、`≈`、部分 emoji。
  动手前可以先扫一遍：`[c for c in set(text) if not try_encode(c,'gbk')]`。

#### 万一已经写坏了，去哪找回来

**`dist/Gleam-release-<date>.zip` 里带一份 `CHANGELOG.md`**（打包脚本会把源码树里的它放进发布合集）。
最近一次打包时的副本通常就是几分钟前的状态：

```bash
unzip -o -q dist/Gleam-release-20260920.zip CHANGELOG.md -d /tmp/chg
cp /tmp/chg/CHANGELOG.md pack/CHANGELOG.md
```

恢复后核对三件事：字节数、`CRLF/LF` 计数、以及关键小节标题还在不在（`grep -c` 几个关键词）。

改完务必验一次没被转成 UTF-8：

```bash
python - <<'EOF'
raw = open('pack/CHANGELOG.md','rb').read()
print('字节数:', len(raw), '| CRLF:', raw.count(b'\r\n'), '| LF:', raw.count(b'\n'))
try:
    raw.decode('utf-8'); print('警告：已被转成 UTF-8')
except UnicodeDecodeError:
    raw.decode('gbk'); print('GBK 保持 ✓')
EOF
```

### 用 Python 批量改 Go 源码的坑

1. **行尾是「逐文件」的，不是「逐仓库」的——动手前先量。**
   2026-09-20 再次修正：此前记的「本仓库全部是 LF」**是错的**。
   实测：`Gleam 技术设计文档.md` 是**统一 CRLF**（768/768），
   而 `README.md`、各 `.go`、`website/*`、`configs/*`、`scripts/*`、`editor-plugin/*` 都是 LF。
   所以**没有任何仓库级行尾结论可以直接套用**：改哪个文件就先量哪个文件。

   批量替换时两头都会踩：
   - Python 在 Windows 下写文本默认把 `\n` 翻成 `\r\n`（把 LF 文件整份改成 CRLF）；
   - 反过来，`newline=''` 读进来、`"\n".join(lines)` 写回去，
     **被替换的那几行会丢掉 `\r`**，把 CRLF 文件搞成 MIXED。

   写文件带 `newline=''`，改完**核一遍**：

   ```bash
   python -c "raw=open('internal/llm/llm.go','rb').read(); print('CRLF', raw.count(b'\r\n'), 'LF', raw.count(b'\n')-raw.count(b'\r\n'))"
   ```

   **合格标准只有两种**：`CRLF == LF`（全 CRLF）或 `CRLF == 0`（全 LF）。
   两者都不是 → 你刚把文件改成了 MIXED，必须修。
   修回全 LF：`python -c "p='x.go'; raw=open(p,'rb').read(); open(p,'wb').write(raw.replace(b'\r\n',b'\n'))"`
   修回全 CRLF：`python -c "p='x.md'; ls=open(p,'rb').read().split(b'\n'); open(p,'wb').write(b'\n'.join(l if (i==len(ls)-1 or l.endswith(b'\r')) else l+b'\r' for i,l in enumerate(ls)))"`

   **任何用 Python 写仓库文件的场合都适用，不只是临时/变异脚本。** 已栽三次：
   2026-09-20 变异脚本用 `io.open(path, encoding='utf-8')` 读 + 同款写回；
   **2026-09-21 给 4 个测试文件批量补参数**，用 `open(p,'w').write(s)` 写回。
   Windows 下**读进来会翻成 `\n`、写回去会翻成 `\r\n`**——于是"还原/改完"的文件整份变成 CRLF，
   `gofmt -l` 当场全红，看起来像是我把代码改坏了（实测：`open(p,'w')` 写出 `\r\n`，
   `open(p,'wb')` 写出 `\n`）。**写法只有两种**：`open(p,'wb')` 自己编码，
   或 `open(p,'w', encoding='utf-8', newline='')`。**改完必须复核行尾 + `gofmt -l`**，还原 ≠ 原样。

   **一个便宜的"只动了行尾"证明法**（比逐行读 diff 快得多）：
   `tr -d '\r' < f > f.check` → `gofmt -w f` → `cmp f f.check`。
   一致即证明 gofmt 只去了 CR、没动内容（第十二批用它确认了 4 个文件）。
   清理 `f.check` 用 shell 的 `rm`。

2. **插入的中文里别用 ASCII 双引号**：`"...或"待补充"字样..."` 会截断 Go 字符串字面量，
   报 `missing ',' in composite literal`。中文引号请用 `「」`（这也是本项目文档的既有习惯）。

3. **按行号批量替换时，"整文件已无残留"的断言必须放在该文件全部替换之后。**
   2026-09-20 踩到：脚本对 `cmd/gleam/main.go` 改完第 6 行就断言"整文件不含 ZCode"，
   而第 106 行还在 → 断言失败 → **整个文件没写盘**，前面那次替换白做。
   正确顺序：**先把一个文件的全部行改完 → 再做残留断言 → 最后写盘**。
   单行替换时用「该行原本包含目标词、替换后不包含」做前置校验，别用它当整文件校验。

### `gofmt -l` 有输出时，先看差异内容再归因

`gofmt -l` 报警有两个完全不同的原因，处置方式也不同：

- **对齐列变了**（真问题，必须修）：新增结构体字段或常量会让整组的 `=` / 注释对齐列平移。
  `gofmt -d` 会显示只差几个空格——这就是它。
- **行尾不一致**：本仓库**确实存在**——`Gleam 技术设计文档.md` 是 CRLF，其余多为 LF。
  批量改文件时最容易踩，详见上面「用 Python 批量改 Go 源码的坑」第 1 条。
- **既有漂移**：**2026-09-21 起已清零**。第十一批做过一次全仓整改，`gofmt -w` 修掉 28 个文件
  （真实缩进漂移 + 7 个文件头 UTF-8 BOM + 2 个 import 重排）。此后
  `gofmt -l ./internal ./pkg ./cmd` 必须**恒为空**。
  历史成因（供排查参考）：文件头 UTF-8 BOM（`\ufeff`，`gofmt -d` 第一条差异就是删它）、
  结构体 tag 对齐列不同、`cmd/gleam/main.go` 的 import 排序。
  **所以现在「`gofmt -l` 有输出」一律算自己的问题，必须修掉再收尾。**
  （旧建议"别 `gofmt -w` 全仓库、免得造出巨大 diff"已作废：本仓库无版本控制，那个 diff 没人看，
  而**最便宜的门禁长期失守**才是真代价——格式检查绿着才有意义。）
- **但门禁命令是 `gofmt -l ./internal ./pkg ./cmd`，不是 `gofmt -l .`。** 后者会多报
  `scripts/make-icon.go`、`scripts/make-zip.go` 两个文件（算术表达式空格 `(200-r)*0.85`，
  2026-09-21 前既存，与本次改动无关）。**`scripts/` 不在门禁范围内**——
  别为了"全仓干净"去动它，那会造出与本次任务无关的改动。
  判断顺序：先用门禁命令跑（必须空）；只有它空了才去管别处的报警。

**判断行尾符必须用 Python 读 bytes（或 `od -c`），绝不能用 `grep`。** 本机 Git Bash 里
`grep -c $'\r' file` 是**假阳性**：它会把每个文件都数成全 CRLF（实测 696 行文件报 696 行 CRLF，
而 Python 数出来 `CRLF=0 / LF=696`）。
**2026-09-21 又栽一次**：用 `grep -c $'\r'` 判 8 个文件，得出"全部 CRLF"的错误结论，
`od -c` 一看只有 4 个真是 CRLF。**这条已栽两次，别再图快。**

但**别把结论反推成「本仓库是 LF-only」**——那是错的：
`Gleam 技术设计文档.md` 实测 `CRLF=768 / LF=768`（统一 CRLF）。
**行尾逐文件不同，必须逐个量，不能拿一个文件的结论去套另一个文件。**

```bash
python -c "
import pathlib
for f in ['internal/agent/readiness.go','cmd/gleam/main.go']:
    b = pathlib.Path(f).read_bytes()
    crlf = b.count(b'\r\n'); print(f, 'CRLF=', crlf, 'LF=', b.count(b'\n')-crlf)
"
```

先看差异，别猜：

```bash
mkdir -p .fmtcheck && for f in <改动文件>; do cp "$f" ".fmtcheck/$(basename $f)"; done
gofmt -l .fmtcheck/     # 仍有输出 → 是真格式问题
gofmt -d .fmtcheck/     # 看具体差在哪
rm -rf .fmtcheck
```

对齐问题要修但**别用 `gofmt -w` 直接写**（可能连带改行尾）。稳妥做法是归一化行尾 → `gofmt -w` → 还原：

```bash
# 先归一化成 LF 的副本 → 在副本上 gofmt → 按原行尾写回。副本清理交给 shell。
python - <<'EOF'
import subprocess, pathlib
for f in ['internal/config/config.go']:
    p = pathlib.Path(f)
    raw = p.read_bytes()
    crlf = raw.count(b'\r\n') > 0            # 先记下原本是什么行尾
    tmp = pathlib.Path('.fmtcheck'); tmp.mkdir(exist_ok=True)
    lf = tmp / p.name
    lf.write_bytes(raw.replace(b'\r\n', b'\n'))
    subprocess.run(['gofmt', '-w', str(lf)], check=True)
    fixed = lf.read_bytes()
    p.write_bytes(fixed.replace(b'\n', b'\r\n') if crlf else fixed)   # 按原行尾写回
    print(f, 'CRLF' if crlf else 'LF', '->', p.read_bytes().count(b'\r\n'))
EOF
rm -rf .fmtcheck
```

**Python 里绝不要用 `os.remove`**：本环境的 safe-delete 垫片会把它劫持成"移入回收站"，
没有桌面会话时直接抛 `SHFileOperationW 失败: 0x2`——崩在 `finally` 里会让整个流程**带伤运行**，
症状极具迷惑性（结果时红时绿、两次不一致）。**清理一律交给 shell 的 `rm`，Python 全程不删文件。**

### 写测试：别复刻生产代码的拼装逻辑

需要断言"拼出来的提示词长什么样"时，**不要**在测试里照着生产代码再拼一遍——
那样生产代码改了测试也不会红。正解是把拼装抽成一个函数（如 `reflectorSystemPrompt(hasChecks, digest)`），
让测试调真的那一个。

另外两个高频坑：

- **`time.Duration` 参数别传字面量**：`NewGLM(..., 5)` 是 5 **纳秒**，测试会以
  "context deadline exceeded" 失败。写 `5*time.Second`。
- **断言"公共前缀"时，段落标题本身是公共的**：两个提示词都有 `## 上下文`，标题自然在公共前缀里，
  只有标题下的**取值**不同。要断言取值，不要断言标题。

### 提示词布局契约要写成测试

前缀缓存要求逐字节相同的最长公共前缀，而布局会被后续改动无意破坏（加个字段、插段上下文就废了）。
改 `buildSystemPrompt` / `reflectorSystemPrompt` 后，跑 `TestPromptCache*`（`internal/agent/promptcache_test.go`）：
它们断言稳定段覆盖哪些块、易变内容不得侵入、长度是否达标。改布局前先读那组测试的注释。

### 清理冒烟临时文件：沙箱的 safe-delete 会「报错但其实删掉了」

这个沙箱拦截删除，而且两种工具的表现不一样，**都不能只看退出码**：

- **`rm -f` / `rm -rf`（Bash）**：无输出、退出码 1、信号 SIGTERM，**文件一个都没删**。
- **`Remove-Item`（PowerShell）**：抛 `[safe-delete][SAFE_DELETE_FAIL_CLOSED] ... "reason":"trash-failed"`，
  但**文件确实被删了**（它想移进回收站，失败后仍然执行了删除）。

所以正确的做法是**删完用一次全新的目录枚举复查**，而不是看命令是否报错：

```powershell
Set-Location D:\PersonalProject\Gleam
$targets = @('.smoke2-fake.py','.smoke2-config.yaml','.smoke2-prompts.log','.smoke2.log',
             '.smoke2-pdata','.smoke2','.pkgs.txt')
foreach ($t in $targets) { if (Test-Path $t) { try { Remove-Item -Force -Recurse $t -ErrorAction Stop } catch { } } }
$left = Get-ChildItem -Force | Where-Object { $_.Name -like '.smoke*' } | Select-Object -ExpandProperty Name
if ($left) { Write-Output ("remaining: " + ($left -join ', ')) } else { Write-Output "clean" }
```

两个坑：

1. **别在 `Remove-Item` 上用 `-ErrorAction SilentlyContinue` 然后只看有没有输出**——
   报错被吞掉时你分不清"删干净了"还是"压根没动"。
2. **枚举结果偶尔滞后**：一次 `Get-ChildItem` 可能还列着已删的文件。用 `ls -a | grep`
   （另一种工具）复核一次再收工。

### 真机冒烟：验证「模型档位」这类路由特性

档位换模型这种事，单测只能证明客户端构造对了，证明不了**真机上请求真的换了 model**。
做法：假 LLM 端点除了记系统提示词，**把 `req["model"]` 也写进日志**，然后按不同角色各提交一个目标，
最后 `grep -a "^MODEL:" .smoke-prompts.log | sort | uniq -c`。预期能看到每个角色的档位模型各若干条，
外加辅助模型（`fast_model`）的若干条（工具快筛走的就是它）。

同理，验证「流式也拿真 usage」时，假端点在收尾块回一个带 `prompt_tokens_details.cached_tokens` 的 usage，然后看任务结果的 `usage.estimated_calls` **是否为 0**——为 0 才说明流式路径真的读到了厂商返回的用量。

**档位还影响提示词**（2026-09-20 起）：`rules.go` 里按档位条件化的规则只在"用户为这一档单独配了模型"
时才生效。所以上面这个冒烟日志除了看 `MODEL:`，还要 grep 一下系统提示词里的那条规则措辞——
`grep -a "不要反复重跑" .smoke-prompts.log` 应该只在配了档位的角色上出现。
另有一个**离线**的量测入口：`gleam eval --tier coding`（模拟生效档位，只改提示词构建、不切换模型，
所以不需要真端点），对比 `--tier` 空值时的「提示词合计」字符数即可。

## 七、加「自检 / 体检」类特性时的项目约定

Gleam 已有两处这类东西：`readiness.go`（九坑就绪体检）与 `chatcheck.go`（对话模式独立自检）。
再加同类特性时，按这套约定走。

### 三条硬约束

1. **只读**：不改配置、不发模型请求、不写文件。写一条带超时的测试把它钉住
   （起 goroutine 跑 `Readiness()`，3 秒不返回即判失败——它本该是纯内存的）。
2. **无数据不装懂**：没有历史数据时给 `warn` 并注明"暂无数据"，不要伪造一个绿勾。
   同理，厂商没返回缓存字段时写"未报告"，**不要编一个"命中率 0%"**。
3. **结论指向动作**：每个非 `pass` 项都要带 `fix`；CLI 末尾把待处理项单独收口成一段，
   否则报告看完就完了。

### 判定要用「根因判据」，不要用「比值」

这条是踩出来的。缓存项最初只看"公共前缀占比"，结果真实环境（21 个内置工具）下
稳定段 2448 字符、布局完全正确，却因为按目标筛出的 schema 尾巴很长、占比只有 46%，
被判成「待改进」——**占比低不等于布局坏**。

改成三级判定，顺序有意为之，并抽成纯函数单独测优先级：

```go
cacheStatus(layoutOK, stable, ratio)  // 1) 布局对不对 → 2) 稳定段够不够大 → 3) 占比高不高
```

推广到任何"健康度打分"：先判**机制有没有坏**，再判**量够不够**，最后才判**效率高不高**。
把不同性质的问题塞进一个比值里，就会产出误导性的结论。

### 体检的实测样本要「同时变两样」

`measurePromptPrefix` 用两个不同目标 **+ 两个不同工作目录**构建提示词。
只变目标的话，如果哪天有人把工作目录挪到了提示词前部，两份样本 cwd 相同、
公共前缀照样能穿过去，这个检查就漏了。**检查易变内容的位置，就得让那份内容真的变。**

### 体检代码本身不能把引擎跑挂

`checkDelivery` 最初在 `a.Gate == nil` 时直接 deref 崩了——而"装配不全"恰恰是体检
最该能用的场景。凡是读子系统的地方都要兜底：

```go
func (a *Agent) effectivePermissionOf(t types.Tool) types.Permission {
    if a.Gate != nil { return a.Gate.EffectivePermission(t) }
    return t.Permission()   // 门控缺席时退回工具自身声明
}
```

### 一个实现，多个入口

`Agent.Readiness()` 是唯一实现，界面 / `gleam doctor [--json]` / `GET /api/readiness` 都读它。
**CLI 入口不要图省事传 `agent.NopNotifier{}`**——`doctor` 最初这么写，第 4 项永远报
"未接入审批通道"，因为体检结论被一个假通道污染了。改用与 `gleam goal` 一致的
`newConsoleNotifier()`，并支持 `--config/--workspace/--data-dir/--mock-llm`，
这样还能对指定部署做体检。用 `splitFlagArgs(args, fs)` 解析参数（允许标志与位置参数混写）。

## 八、改前端时的验收方式

### 接线完整性要写成测试（最容易悄悄上线的一类缺陷）

后端路由通了、测试也过了，但界面没有入口——用户根本看不到。所以加一条测试，
从 `staticFS` 读内嵌的 `static/index.html` / `app.js` / `style.css`，断言：

- HTML 里有 `data-view="<name>"` 与 `id="view-<name>"` 及关键元素 id；
- JS 的 `VIEW_LOADERS` 里注册了 `<name>: loadXxx`（**只加视图不加这行，点导航不会加载**）；
- JS 里有渲染函数与接口路径；
- CSS 里有新加的类名。

### 沙箱里浏览器起不来

`agent-browser open <url>` 在本机沙箱被 SIGTERM 拦掉（重试两次均失败），**渲染截图做不了**。
降级成三重验证，够用：

1. `node --check internal/webui/static/app.js` 语法校验；
2. 起 `gleam webui --mock-llm --data-dir <临时目录>`，`curl` 校验
   `/api/<新路由>` 返回 200 且内容正确，`/`、`/assets/app.js`、`/assets/style.css` 里能 grep 到新接线；
3. 上面那条接线测试。

**`curl` 要加 `--noproxy '*'`**：本机配了代理，直接 curl `127.0.0.1` 会返回
`upstream connect failed: 由于目标计算机积极拒绝`，看起来像服务没起来，其实是走了代理。

**起服务要用 `run_in_background: true`，不要在命令末尾加 `&`**：加了 `&` 之后 shell 立刻返回，
子进程会被一起收走（日志里能看到"已启动"，但进程和端口都没了）。

## 十、加「记录这次用了什么」这类特性时的约定

参考 ④「规则集身份」（`RuleSetVersion` + `ActiveRuleIDs` + `gleam rules`）。这类特性
不参与业务逻辑，一旦写歪不会报错、只会误导归因，所以约定比实现更重要：

1. **记录必须与渲染同源。** 共用一个选择函数（这里是 `selectRules`），并写**双向**一致性测试：
   记在生效列表里的，正文必须出现在提示词里；没记进去的（含同一 ID 未被选中的变体），
   正文必须**不出现**。一份会撒谎的记录比没有更糟——它让人在错误的方向上排查。
2. **身份用内容派生的指纹，不手写版本号。** 手写版本会忘改，而"这次用的是哪版"
   只在内容真变了时才有意义。所有会影响行为的字段都要进指纹（含适用范围与顺序）。
3. **只报，不卡门禁。** 观测类变化（规则集版本、模型名）只写进回归报告，不改退出码。
   卡它会逼人把 `--strict` 摘掉，门禁整体失效——那个代价比漏报大得多。
4. **区分不了的差异要写出来。** 如果某条记录结构上就看不见某个差异（ID 列表区分不了
   共用 ID 的两个变体），把它写成显式边界断言，别留给读者自己假设。
   判断方法：做变异验证时若"改坏了却全绿"，先问断言看不看得见，再决定加不加断言。
5. **落点要成链**：产出结果 → 成长日志 → 评测基线 → 离线回查命令。
   断掉任何一环，"换了规则之后这批历史是哪一版跑出来的"就答不出来。

## 九、环境瞬时故障：重试，别改代码

`go test ./...` 偶尔会报 `package encoding/asn1 is not in std`、
`package internal/race is not in std`、`package internal/goexperiment is not in std`
这类 GOROOT 报错，或者整个命令被 SIGTERM。这是环境瞬时故障，
**原样重试一次就恢复**。不要据此去改 `go.mod`、`GOROOT` 或代码。
