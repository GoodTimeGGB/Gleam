#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""出网落点与台账清单必须双向对上：记了却没登记、登记了却没记，都算红。

**为什么要有**：本地优先的产品最坏的失效不是"连得少"，而是**"连了而没交代"**——
那句话在界面上的形状和"没连"一模一样。「连接与出网」台账（`internal/agent/connections.go`）
承诺"每一条出网路径都有一行"，而这个承诺要靠两条线同时成立：
  * 代码里真的有 `RecordEgress("<kind>", …)` 把这一类留痕送进门控；
  * 台账清单 `egressKinds` 里有这一类，于是它会被画成一行、有读数、有关闭入口。
只写其中一条的人不觉得自己错了：函数建了、行也画了，测试全绿，缺的是中间那根线。
这正是本仓库栽过的那一类（AGENTS.md 铁律 3：每条可机械判断的规则都要有 exit non-zero 的命令）。

**判据三条**：
1. 正向：全仓每个 `RecordEgress("<字面量>"` 调用点的 kind，必须在 `egressKinds` 里。
   新接一条出网路径忘了登记 → 红。
2. 反向：`egressKinds` 里每一项，必须真有至少一个调用点。
   登记了一条代码里没人记的出网 → 红（那一行界面上永远显示"没有记录"，
   而它的真实含义是"这台机器压根没往这一类记"——两者在读法上分不出来）。
3. kind 必须是字符串字面量。`RecordEgress(kind, …)` 这种传变量的写法，本闸门与台账
   都无从对账，所以**当场拦**：宁可要求写死，也不要留一个"看起来有其实数不出"的落点。
4. 台账写给用户看的字符串里不许有 markdown 强调标记（`**…**`）。这张表走
   `textContent` 呈现，星号不会被渲染成粗体，而是**原样出现在界面上**——
   真机上就发生过：那一格显示成「它是只读工具，**默认不经过你的批准**」。
   判据只看字符串字面量（注释已剥掉），因为这个文件里的注释本来就在大量使用 `**`。

**为什么读源码前先剥注释**：台账的文档注释里会提 `RecordEgress` 这个函数名（它就该提，
那是在交代留痕在哪）。不剥注释的话，一句注释凭空多出一个"落点"，判据第一天就开始说谎。
剥注释的实现不在本文件——它和 `check-cue-owner.py` 用的是同一件事，住在 `scripts/_gocomment.py`，
那里说明了为什么必须是字符状态机而不是正则。

**为什么排除 `_test.go`**：测试里的 kind 是造数据用的（`egress_test.go` 拿 `file.write`
当反例），不是这台机器真会发出去的落点。把它们算进来，反向判据立刻红在一行假账上。

**怎么改才通过**：新接一条出网路径时，在 `internal/agent/connections.go` 的 `egressKinds`
里加一项，并给台账加对应的一行（`TestConnections_EveryEgressKindHasARow` 会在运行时再判一次
"这一行今天到底画没画出来"——闸门只能读源码，读不出运行态）。

**自我失效的防线**：解析不到 `egressKinds`、一条调用点都没扫到，都直接失败——
宁可红着，也不给"已通过"的错觉。

**负例控制自带**：`--self-test` 现造五份坏文本（新增落点没登记 / 登记了没人记 /
kind 传变量 / 清单解析不出 / 文案里带 markdown 强调）断言会被判红，再拿两份**长得像**的
干扰文本（只在注释里提 `RecordEgress`、注释里用 `**`）断言不许误报。

用法：python scripts/check-egress-owner.py [--self-test] [仓库根目录]
退出码：0 = 双向对上、没有传变量的落点、文案没有 markdown 标记；1 = 否则。
"""
import io
import os
import re
import sys

import _gocomment
import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

OWNER_FILE = os.path.join("internal", "agent", "connections.go")
CALL_NAME = "RecordEgress"
SKIP_DIRS = {".git", "bin", "pack", "dist", "node_modules", "static"}

KINDS_RE = re.compile(r"var\s+egressKinds\s*=\s*\[\]string\{(.*?)\}", re.S)
STR_RE = re.compile(r'"((?:[^"\\]|\\.)*)"')


# 剥注释这件事的 owner 在 scripts/_gocomment.py（两道闸门共用），这里只留一个名字。
strip_go_comments = _gocomment.strip


def first_arg(src, open_paren):
    """取 `(` 之后第一个顶层实参（按嵌套与字符串走，遇到顶层逗号停）。"""
    i = open_paren + 1
    depth = 0
    buf = []
    while i < len(src):
        c = src[i]
        if c in "([{":
            depth += 1
        elif c in ")]}":
            if depth == 0:
                break
            depth -= 1
        elif c == "," and depth == 0:
            break
        buf.append(c)
        i += 1
    return "".join(buf).strip()


def scan_sites(docs):
    """docs = [(标签, 已剥注释的源码)] → (字面量落点 [(kind, 标签:行)], 传变量的落点 [...])。"""
    literals, variables = [], []
    for label, src in docs:
        for m in re.finditer(r"\b%s\(" % CALL_NAME, src):
            before = src[:m.start()]
            # 跳过函数定义本身（`func (g *Gate) RecordEgress(kind, host …)`）：
            # 它的第一参数按定义就是变量，把它算成"传变量的落点"会在第一天就红在自己身上。
            if re.search(r"func\s+(?:\([^)]*\)\s*)?$", before):
                continue
            arg = first_arg(src, m.end() - 1)
            lit = STR_RE.fullmatch(arg)
            line = src[:m.start()].count("\n") + 1
            if lit:
                literals.append((lit.group(1), "%s:%d" % (label, line)))
            else:
                variables.append(("%s:%d" % (label, line), arg[:40]))
    return literals, variables


def owner_kinds(owner_src):
    """从 owner 文件（已剥注释）里取 egressKinds。解析不出返回 None 让调用方判红。"""
    m = KINDS_RE.search(owner_src)
    if not m:
        return None
    return STR_RE.findall(m.group(1))


def markdown_in_texts(src):
    """找出**字符串字面量**里带 markdown 强调的行（注释已在外面剥掉，不算）。

    为什么只扫字符串：这个文件的文档注释大量使用 `**` 来标重点，那是给人读源码的人写的；
    而字符串是要上界面的，`textContent` 不认 markdown，星号会原样出现在表格里。
    """
    hits = []
    for m in STR_RE.finditer(src):
        if "**" in m.group(0):
            hits.append((src[:m.start()].count("\n") + 1, m.group(1)[:56]))
    return hits


def judge(kinds, literals, variables, md_hits, say):
    """判据只写这一份：真文件与负例共用同一套判断，否则负例证的不是真判据。"""
    rc = 0
    if kinds is None:
        say("解析不出 egressKinds（owner：%s）：判据失效，请先修本脚本或补回清单。"
            % OWNER_FILE.replace("\\", "/"))
        return 1
    if not kinds:
        say("egressKinds 是空的：那这张台账就没有任何出网行，判据也无从对账——红。")
        return 1
    if not literals and not variables:
        say("全仓一个 %s 调用点都没扫到：留痕接线被删了还是改了名？检查本身失效。" % CALL_NAME)
        return 1

    listed = set(kinds)
    recorded = set(k for k, _ in literals)

    unlisted = sorted(k for k in recorded if k not in listed)
    if unlisted:
        say("这些出网落点记了留痕，却不在台账清单 egressKinds 里（界面上没有这一行，"
            "等于连了没交代）：")
        for k in unlisted:
            where = [loc for kk, loc in literals if kk == k]
            say("  %s ← %s" % (k, "、".join(where)))
        rc = 1

    unrecorded = sorted(k for k in listed if k not in recorded)
    if unrecorded:
        say("台账清单里登记了这些出网落点，代码里却没有任何一处记它（那一行的读数永远说"
            "\"没有记录\"，而真实原因是\"没往这一类记\"）：")
        for k in unrecorded:
            say("  %s" % k)
        rc = 1

    if variables:
        say("这些 %s 调用点的 kind 不是字符串字面量，闸门与台账都无从对账——请写死：" % CALL_NAME)
        for loc, arg in variables:
            say("  %s  first arg = %s" % (loc, arg))
        rc = 1

    if md_hits:
        say("台账文案里有 markdown 强调标记：这张表按纯文本呈现，星号会原样显示在界面上——")
        for line, snippet in md_hits:
            say("  %s:%d  「%s」" % (OWNER_FILE.replace("\\", "/"), line, snippet))
        rc = 1

    if rc == 0:
        say("  出网落点对上：%d 条登记、%d 个字面量调用点、无传变量的落点、文案无 markdown 标记"
            % (len(listed), len(literals)))
    return rc


def go_files(root):
    docs = []
    for dirpath, dirnames, names in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS)
        for n in sorted(names):
            if not n.endswith(".go") or n.endswith("_test.go"):
                continue
            path = os.path.join(dirpath, n)
            label = os.path.relpath(path, root).replace("\\", "/")
            try:
                with io.open(path, "rb") as f:
                    src = f.read().decode("utf-8", "replace")
            except OSError as e:
                raise RuntimeError("读不到 %s：%s" % (label, e))
            docs.append((label, strip_go_comments(src)))
    return docs


def self_test() -> int:
    """四份坏文本必须拦住 + 一份干扰文本不许误报。"""
    head = "var egressKinds = []string{\"llm\", \"web.fetch\", \"cloud\", \"feedback\"}\n"
    good_sites = ('a := "x"\n'
                  'llm.SetEgressHook(func(host string, n int) { gate.RecordEgress("llm", host, n) })\n'
                  'webTool.OnEgress = func(host string, n int) { gate.RecordEgress("web.fetch", host, n) }\n'
                  'authMgr.OnEgress = func(host string, n int) { gate.RecordEgress("cloud", host, n) }\n'
                  's.Agent.Gate.RecordEgress("feedback", host, n)\n')
    sink = []
    if judge(owner_kinds(strip_go_comments(head)), *scan_sites([("main.go", good_sites)]),
             markdown_in_texts(strip_go_comments(head) + good_sites), sink.append) != 0:
        print("真形状被判红，self-test 无从对照：\n  " + "\n  ".join(sink))
        return 1

    bad = [
        ("新增落点没登记", head, 'gate.RecordEgress("telemetry", host, n)\n'),
        ("登记了却没人记", 'var egressKinds = []string{"llm", "web.fetch", "cloud", "feedback", "push"}\n',
         good_sites),
        ("kind 传变量", head, good_sites + 'gate.RecordEgress(kindArg, host, n)\n'),
        ("清单解析不出", 'var somethingElse = []string{"llm"}\n', good_sites),
        ("文案带 markdown 强调", head, good_sites + 'r.Trigger = "它是只读工具，**默认不经过你的批准**"\n'),
    ]
    rc = 0
    for name, kinds_src, sites_src in bad:
        sink = []
        kinds = owner_kinds(strip_go_comments(kinds_src))
        literals, variables = scan_sites([("case.go", strip_go_comments(sites_src))])
        md_hits = markdown_in_texts(strip_go_comments(kinds_src + sites_src))
        if judge(kinds, literals, variables, md_hits, sink.append) == 0:
            print("负例没被拦住：%s —— 判据在永远点头，本脚本要修" % name)
            rc = 1
        else:
            print("  拦住：%s（%s）" % (name, sink[0][:56]))

    # 干扰文本一：注释里提一句这个函数名（台账的文档注释就是这么写的），不许凭空多出一个落点。
    decoy_src = ('// 门控留痕见 gate.%s("cloud", host, n)——注释不是调用点。\n' % CALL_NAME) + head
    sink = []
    kinds = owner_kinds(strip_go_comments(decoy_src))
    literals, variables = scan_sites([("comment.go", strip_go_comments(decoy_src))])
    if literals:
        print("注释里的调用点被当成了真落点：%s —— 剥注释没生效" % literals)
        rc = 1
    else:
        print("  不误报：注释里提到的 %s 不算调用点" % CALL_NAME)

    # 干扰文本二：注释里用 `**` 标重点（这个文件的注释本来就在这么写），不许判红。
    # 注释是写给读源码的人的，只有字符串会上界面——判据混在一起，第一天就红在满屏假告警上。
    decoy_md = '// egressKinds 出网落点清单，**这一份是 owner**。\n' + head
    hits = markdown_in_texts(strip_go_comments(decoy_md))
    if hits:
        print("注释里的 markdown 标记被当成了文案：%s —— 剥注释没生效" % hits)
        rc = 1
    else:
        print("  不误报：注释里的 **强调** 不算界面文案")
    return rc


def main() -> int:
    argv = [a for a in sys.argv[1:] if a != "--self-test"]
    selftest = "--self-test" in sys.argv[1:]
    root = argv[0] if argv else "."
    try:
        docs = go_files(root)
    except RuntimeError as e:
        print(str(e))
        return 1
    if not docs:
        print("一个 .go 文件都没扫到（%s）：检查本身失效，请先修本脚本。" % root)
        return 1

    owner_label = OWNER_FILE.replace("\\", "/")
    owner_src = None
    for label, src in docs:
        if label == owner_label:
            owner_src = src
            break
    if owner_src is None:
        print("扫到了 %d 个 .go 文件却没有 %s：清单搬家了？判据失效。" % (len(docs), owner_label))
        return 1

    kinds = owner_kinds(owner_src)
    literals, variables = scan_sites(docs)
    # markdown 判据只扫 owner 文件：台账的每一句面向用户的话都住在那儿，
    # 扫全仓会把别处（真正会渲染 markdown 的地方）的写法也算成红。
    rc = judge(kinds, literals, variables, markdown_in_texts(owner_src), print)
    if rc != 0 or not selftest:
        return rc
    return self_test()


if __name__ == "__main__":
    sys.exit(main())
