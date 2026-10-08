#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""候补目标那一层：线索清单、派生分支、人话牌子三处必须对上，而且**这一层不许有手**。

**为什么要有**：批次 F17 把「主动性」的口径写成三句话（只提议不执行 / 卡片不落盘 / 不调模型，
设计文档 §4.6.35）。三句话都只有读源码才能证明——它们描述的正是"这里没有发生的事"，
所以行为测试天生测不到（没有输出、没有副作用、没有可断言的状态变化）。
本仓库的经验是同一条：**只活在注释里的规则，下次重构就没了**。所以每条都要有 exit non-zero。

**判据三条**：
1. **三份清单双向对账**：`cueSignals`（owner：登记了哪几条线索）、`deriveCues` 里的
   `case "…"`（派生：真的各浮得出一次卡）、`cueSignalText` 里的 `case "…"`（人话牌子）。
   三个方向都会漂，而且每一种漂法在界面上都不像错误：
   * 登记了没派生 → 那一类永远不会出现，清单变成许愿单（README 却说得出这三条）；
   * 派生了没登记 → 这一页说的"只从这几条线索里想"当场成假话，多出来的那条没人负责；
   * 派生了没牌子 → `cueSignalText` 原样返回枚举值，界面上裸奔成 `half_done`（M3 那条老坑）。
2. **提议层没有手**：`internal/agent/cues.go`（已剥注释）里不许出现 `RunGoal(` / `TriggerNow(` /
   `Revert(` / `atomicfile.Write(`。候补目标一旦自己提交任务、自己还原改动、自己往盘上写卡片，
   第 1 条口径就不是"约定"而是"曾经成立过"。为什么只扫 `cues.go`：处置记录那份
   （`cues_store.go`）的职责**就是**把人的决定写下去，`atomicfile.Write` 是它该调的。
3. **自作文案不许带 markdown 强调**（`**…**`）：卡片与「已按下」列表都走 `textContent`，
   星号不会变成粗体，而是**原样出现在界面上**——这条真机上抓过第二次（§4.6.34 第 ④ 条）。

**为什么读源码前先剥注释**：这一层的注释本来就要提 `RunGoal`（它在解释"为什么不调它"），
本来就要用 `**` 标重点。不剥注释，判据第一天就红在自己身上。实现见 `scripts/_gocomment.py`。

**这条判据自己也会失效**，所以：解析不出 `cueSignals`、`deriveCues` 里一个 `case` 都扫不到、
owner 文件不存在，全部直接失败——宁可红着，也不给"已通过"的错觉。

**闸门读不出"今天到底浮没浮出一张卡"**，那半归 Go：`TestCues_EverySignalProducesARow`
每条线索各造一份历史，断言都浮得出来；`TestCues_LabelsCoverEveryEnumValue` 断言每条线索
与每种处置原因都有人话。两边合起来才是"三份清单对上"。

**负例控制自带**：`--self-test` 现造六份坏文本（登记没派生 / 派生没登记 / 派生没牌子 /
提议层伸了手 / 文案带 markdown / 清单解析不出）断言会被判红，再拿两份**长得像**的干扰文本
（注释里提 `RunGoal`、注释里用 `**`）断言不许误报。

用法：python scripts/check-cue-owner.py [--self-test] [仓库根目录]
退出码：0 = 三份清单对上、提议层没有手、文案无 markdown（`--self-test` 时：每份负例都被拦住）；1 = 否则。
"""
import io
import os
import re
import sys

import _gocomment
import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

OWNER_FILE = os.path.join("internal", "agent", "cues.go")
STORE_FILE = os.path.join("internal", "agent", "cues_store.go")

# 提议层不许碰的入口：提交任务、叫调度器立刻跑、还原改动、把卡片写进盘。
HANDS = ("RunGoal(", "TriggerNow(", "Revert(", "atomicfile.Write(")

SIGNALS_RE = re.compile(r"var\s+cueSignals\s*=\s*\[\]string\{(.*?)\}", re.S)
STR_RE = re.compile(r'"((?:[^"\\]|\\.)*)"')
CASE_RE = re.compile(r'\bcase\s+"((?:[^"\\]|\\.)*)"\s*:')


def func_body(src, name):
    """取顶层函数 `func name(` 到下一个顶层 `func` 之间的正文（gofmt 保证顶层 func 顶格）。

    为什么按函数取而不按整个文件取：`case "…"` 在别的 switch 里也会出现（`cueKindText`
    就是），全文件扫会把失败类别的取值当成线索，对账立刻假通过。
    """
    m = re.search(r"^func\s+%s\(" % re.escape(name), src, re.M)
    if not m:
        return None
    rest = src[m.end():]
    nxt = re.search(r"^func\s", rest, re.M)
    return rest[:nxt.start()] if nxt else rest


def signal_cases(src, name):
    body = func_body(src, name)
    return None if body is None else CASE_RE.findall(body)


def registered_signals(src):
    m = SIGNALS_RE.search(src)
    return None if not m else STR_RE.findall(m.group(1))


def hands_in(src):
    """提议层里出现的执行入口（已剥注释）。行号留给报错用。"""
    return [(hand, src[:m.start()].count("\n") + 1)
            for hand in HANDS for m in re.finditer(re.escape(hand), src)]


def markdown_in_texts(src):
    """找**字符串字面量**里带 markdown 强调的行（注释已在调用方剥掉，不算）。"""
    return [(src[:m.start()].count("\n") + 1, m.group(1)[:56])
            for m in STR_RE.finditer(src) if "**" in m.group(0)]


def judge(signals, derived, labeled, hands, md_hits, say):
    """判据只写这一份：真文件与负例共用同一套判断，否则负例证的不是真判据。"""
    rc = 0
    owner = OWNER_FILE.replace("\\", "/")
    if signals is None:
        say("解析不出 cueSignals（owner：%s）：线索清单搬家了还是改了写法？判据失效，请先修本脚本。" % owner)
        return 1
    if not signals:
        say("cueSignals 是空的：那这一页就没有任何线索，候补区永远只有空态——判据也无从对账，红。")
        return 1
    if derived is None:
        say("找不到 func deriveCues：%s 的派生入口改名了？判据失效。" % owner)
        return 1
    if not derived:
        say("deriveCues 里一个 `case \"…\":` 都扫不到：线索不再按 cueSignals 逐条派生了？"
            "改成别的写法本闸门就无从对账——请连同本脚本一起改。")
        return 1
    if labeled is None:
        say("找不到 func cueSignalText：%s 的牌子（人话）出口改名了？判据失效。" % owner)
        return 1

    reg, der = set(signals), set(derived)
    lab = set(labeled)

    only_listed = sorted(reg - der)
    if only_listed:
        say("这些线索登记在 cueSignals 里，deriveCues 却没有对应分支（界面上永远不会出现这一类，"
            "清单成了许愿单）：")
        for s in only_listed:
            say("  %s" % s)
        rc = 1

    only_derived = sorted(der - reg)
    if only_derived:
        say("deriveCues 里派生了这些线索，cueSignals 却没登记（这一页说的「只从 cueSignals 那几条里想」"
            "当场成假话，而且多出来那条没人负责）：")
        for s in only_derived:
            say("  %s" % s)
        rc = 1

    no_label = sorted((reg | der) - lab)
    if no_label:
        say("这些线索浮得出卡，cueSignalText 里却没有对应分支（界面上那张牌子会裸奔成枚举值，"
            "M3 那条老坑）：")
        for s in no_label:
            say("  %s" % s)
        rc = 1

    if hands:
        say("提议层出现了执行入口——候补目标**只许提议**（§4.6.35 口径 1：这一层没有手）：")
        for hand, line in hands:
            say("  %s:%d  %s" % (owner, line, hand))
        rc = 1

    if md_hits:
        say("候补目标文案里有 markdown 强调标记：卡片按纯文本呈现，星号会原样显示在界面上——")
        for line, snippet in md_hits:
            say("  %s:%d  「%s」" % (owner, line, snippet))
        rc = 1

    if rc == 0:
        say("  候补目标三处对上：%d 条线索、%d 个派生分支、%d 句牌子；提议层没有手；文案无 markdown 标记"
            % (len(reg), len(der), len(lab)))
    return rc


def read(path):
    try:
        with io.open(path, "rb") as f:
            return f.read().decode("utf-8", "replace")
    except OSError as e:
        raise RuntimeError("读不到 %s：%s" % (path, e))


def evaluate(root):
    """对真仓库算一次判定。owner 文件不在就判红——搬家不算通过。"""
    owner_path = os.path.join(root, OWNER_FILE)
    if not os.path.exists(owner_path):
        print("找不到 %s：候补目标那一层被删了还是搬走了？判据失效。" % OWNER_FILE.replace("\\", "/"))
        return 1
    cues = _gocomment.strip(read(owner_path))
    store = _gocomment.strip(read(os.path.join(root, STORE_FILE)))
    return judge(registered_signals(cues), signal_cases(cues, "deriveCues"),
                 signal_cases(cues, "cueSignalText"), hands_in(cues),
                 markdown_in_texts(cues) + markdown_in_texts(store), print)


def self_test() -> int:
    """六份坏文本必须拦住 + 两份干扰文本不许误报。"""
    good = '''var cueSignals = []string{"repeat_failure", "half_done", "tool_failure"}

func deriveCues(results []any, kept *cueStore) []CueRow {
\tfor _, signal := range cueSignals {
\t\tswitch signal {
\t\tcase "repeat_failure":
\t\t\trows = append(rows, repeatFailureRows(groups)...)
\t\tcase "half_done":
\t\t\trows = append(rows, halfDoneRows(groups)...)
\t\tcase "tool_failure":
\t\t\trows = append(rows, toolFailureRows(kinds)...)
\t\t}
\t}
\treturn alive
}

func cueSignalText(signal string) string {
\tswitch signal {
\tcase "repeat_failure":
\t\treturn "同一个目标反复失败"
\tcase "half_done":
\t\treturn "同一个目标每次只做一半"
\tcase "tool_failure":
\t\treturn "同一类失败反复出现"
\t}
\treturn signal
}
'''

    def verdict(src):
        src = _gocomment.strip(src)
        sink = []
        rc = judge(registered_signals(src), signal_cases(src, "deriveCues"),
                   signal_cases(src, "cueSignalText"), hands_in(src),
                   markdown_in_texts(src), sink.append)
        return rc, sink

    if verdict(good)[0] != 0:
        print("真形状被判红，self-test 无从对照：\n  " + "\n  ".join(verdict(good)[1]))
        return 1

    bad = [
        ("登记了没派生", good.replace('var cueSignals = []string{"repeat_failure", '
                                      '"half_done", "tool_failure"}',
                                      'var cueSignals = []string{"repeat_failure", '
                                      '"half_done", "tool_failure", "calendar_sync"}')),
        ("派生了没登记", good.replace('\t\tcase "tool_failure":',
                                      '\t\tcase "tool_failure":\n\t\t\trows = append(rows, x()...)\n\t\tcase "sneaky":')),
        ("派生了没牌子", good.replace('\tcase "half_done":\n\t\treturn "同一个目标每次只做一半"\n', '')),
        ("提议层伸了手", good + 'func (a *Agent) cueNow() { a.RunGoal(ctx, req) }\n'),
        ("文案带 markdown 强调", good + 'var t = "这张卡**很重要**"\n'),
        ("清单解析不出", good.replace('var cueSignals = []string{', 'var cueKinds = map[string]bool{')),
    ]
    rc = 0
    for name, src in bad:
        code, sink = verdict(src)
        if code == 0:
            print("负例没被拦住：%s —— 判据在永远点头，本脚本要修" % name)
            rc = 1
        else:
            print("  拦住：%s（%s）" % (name, sink[0][:56]))

    # 干扰文本一：注释里提 `RunGoal`（cues.go 的文件头注释就是在解释"为什么不调它"）。
    decoy_hand = '// **只提议，不执行**：这一层不调 RunGoal(ctx, req)，也不 TriggerNow。\n' + good
    code, sink = verdict(decoy_hand)
    if code != 0 and any("执行入口" in s for s in sink):
        print("注释里提到的 RunGoal 被当成了伸手：%s —— 剥注释没生效" % sink)
        rc = 1
    else:
        print("  不误报：注释里提到的 RunGoal 不算伸手")

    # 干扰文本二：注释里用 `**` 标重点（这个文件的注释本来就在这么写）。
    decoy_md = '// cueSignals 三条线索，**信号类型的唯一 owner**。\n' + good
    if markdown_in_texts(_gocomment.strip(decoy_md)):
        print("注释里的 markdown 标记被当成了文案 —— 剥注释没生效")
        rc = 1
    else:
        print("  不误报：注释里的 **强调** 不算界面文案")
    return rc


def main() -> int:
    argv = [a for a in sys.argv[1:] if a != "--self-test"]
    selftest = "--self-test" in sys.argv[1:]
    root = argv[0] if argv else "."
    try:
        rc = evaluate(root)
    except RuntimeError as e:
        print(str(e))
        return 1
    if rc != 0 or not selftest:
        return rc
    return self_test()


if __name__ == "__main__":
    sys.exit(main())
