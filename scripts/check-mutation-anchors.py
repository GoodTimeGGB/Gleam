#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""变异批次的锚点必须还指着源码：每条 `edits` 的 old 在目标文件里恰好出现一次。

**为什么要有**：变异批次是"判据挡不挡得住"的唯一证明，而它自己会**随源码重构静默失效**。
批次 A 真发生过一次：锚点写的是 `dir := filepath.Join(dataDir, "replays")`，那行代码
早被收进 `replayFile()`，于是预检报"出现 0 次"、整批拒绝运行——20 条负例控制
一条都没跑，而 `verify.sh` 六层全绿。跑批次是分钟级、每批一次的人工动作，
没人会在每次重构后想起来重跑；所以要把**便宜的那一半**（锚点还在不在）搬进第 1 层。

**为什么只判锚点、不跑变异**：跑一批十几分钟，进闸门就等于每次改动都付这个钱；
而"锚点失配"是纯文本判断，毫秒级。变异本身仍然每批手动跑
（见 `scripts/mutation/README.md`「它不在 verify.sh 里」）。

**判据不写第二遍**：数锚点、查 `|` 的实现在 `scripts/mutation/_harness.preflight`，
本脚本直接调它。两处各写一版就会漂移——那样闸门点头而批次仍然拒绝运行，
比没有闸门更坏（AGENTS.md 铁律 1）。

**为什么按模块导入、不用正则读源码**：锚点是 Python 字符串字面量，带 `\\t`、`\\n`
转义与跨行拼接。正则会读到转义前的文本，于是"数出来 0 次"这件事本身成了假告警。
导入拿到的就是变异真正会用的那份字符串。

**自我失效的防线**：一个批次都没扫到、某批没导出 `MUTATIONS`，都直接判红——
"没跑但报通过"是这里最坏的结果。

用法：python scripts/check-mutation-anchors.py [--self-test] [仓库根目录]
退出码：0 = 全部锚点各出现一次；1 = 有失配（逐条打印）或检查自身失效。
"""
import importlib.util
import os
import re
import shutil
import sys
import tempfile

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

MUTDIR = os.path.join("scripts", "mutation")


def load_batches(root):
    """导入每个 batch-*.py，取出它的 MUTATIONS。返回 [(批次文件名, 变异列表)] 或 None。

    导入是安全的：批次脚本只在 `__name__ == "__main__"` 下才 run_batch，
    而这里给的是合成模块名，所以只读清单、不会真改源码。
    """
    d = os.path.join(root, MUTDIR)
    try:
        names = sorted(n for n in os.listdir(d) if n.startswith("batch-") and n.endswith(".py"))
    except OSError as e:
        print("读不到 %s：%s" % (MUTDIR.replace("\\", "/"), e))
        return None
    if not names:
        print("在 %s 里没找到任何 batch-*.py：检查本身失效，请先修本脚本。" % MUTDIR.replace("\\", "/"))
        return None

    # 批次脚本自己会 import _utf8（在 scripts/）与 _harness（在 scripts/mutation/）
    sys.path.insert(0, os.path.abspath(os.path.join(root, "scripts")))
    sys.path.insert(0, os.path.abspath(d))
    sys.path.insert(0, os.path.abspath(os.path.dirname(os.path.abspath(__file__))))

    out = []
    for n in names:
        path = os.path.join(d, n)
        mod_name = "mutbatch_" + re.sub(r"\W", "_", n[:-3])
        spec = importlib.util.spec_from_file_location(mod_name, path)
        mod = importlib.util.module_from_spec(spec)
        try:
            spec.loader.exec_module(mod)
        except Exception as e:  # 导入不了 = 这批从来没跑过，比锚点失配更早一步坏掉
            print("%s 导入失败：%s" % (n, e))
            return None
        muts = getattr(mod, "MUTATIONS", None)
        if not isinstance(muts, list) or not muts:
            print("%s 没导出非空的 MUTATIONS：这批变异等于不存在。" % n)
            return None
        out.append((n, muts))
    return out


def judge(root, batches):
    """batches = [(批次名, 变异列表)] → 问题列表。判据只此一份：转调 _harness.preflight。"""
    from _harness import Harness, preflight

    problems = []
    h = Harness(root)
    for label, muts in batches:
        for mut in muts:
            try:
                for p in preflight(h, [mut]):
                    problems.append("%s → %s" % (label, p))
            except OSError as e:
                # 目标文件被改名或删掉：变异无处可施，同样是静默失效
                problems.append("%s → %s：目标文件读不到（%s）" % (label, mut.get("name", "?"), e))
    return problems


def self_test() -> int:
    """坏清单必须判红、正常清单不许误报——判据自己也要被证明。"""
    root = tempfile.mkdtemp(prefix="gleam-anchor-selftest-")
    try:
        os.makedirs(os.path.join(root, "internal"))
        anchor = "\treturn filepath.Join(dataDir, \"replays\")\n"
        with open(os.path.join(root, "internal", "one.go"), "wb") as f:
            f.write(("package x\n\nfunc a() string {\n" + anchor + "}\n").encode("utf-8"))
        with open(os.path.join(root, "internal", "two.go"), "wb") as f:
            f.write(("package x\n\nfunc a() string {\n" + anchor + "}\n\nfunc b() string {\n"
                     + anchor + "}\n").encode("utf-8"))

        good = {"name": "ok", "file": "internal/one.go", "edits": [(anchor, "\treturn \"\"\n")],
                "targets": [("agent", "go test ./internal/agent/ -count=1 -timeout 90s")]}
        bad_cases = [
            ("锚点已被重构走（出现 0 次）",
             {"name": "rotted", "file": "internal/one.go",
              "edits": [("\tdir := filepath.Join(dataDir, \"replays\")\n", "\tx\n")],
              "targets": good["targets"]}),
            ("锚点匹配到两处",
             {"name": "twice", "file": "internal/two.go", "edits": good["edits"],
              "targets": good["targets"]}),
            ("目标文件不存在",
             {"name": "gone", "file": "internal/nope.go", "edits": good["edits"],
              "targets": good["targets"]}),
            ("目标命令含 `|`（cmd.exe 会当管道，命令压根没跑）",
             {"name": "pipe", "file": "internal/one.go", "edits": good["edits"],
              "targets": [("agent", "go test ./internal/agent/ -run 'A|B' -timeout 90s")]}),
        ]
        rc = 0
        for why, mut in bad_cases:
            hits = judge(root, [("batch-selftest.py", [mut])])
            if not hits:
                print("负例没被拦住：%s —— 判据在永远点头，本脚本要修" % why)
                rc = 1
            else:
                print("  拦住：%s（%s）" % (why, hits[0][:96]))
        hits = judge(root, [("batch-selftest.py", [good])])
        if hits:
            print("正常清单被误报：%s" % hits[0][:96])
            rc = 1
        else:
            print("  不误报：锚点恰好一次的正常变异")
        return rc
    finally:
        shutil.rmtree(root, ignore_errors=True)


def main() -> int:
    args = sys.argv[1:]
    selftest = "--self-test" in args
    argv = [a for a in args if a != "--self-test"]
    root = argv[0] if argv else "."
    root = os.path.abspath(root)

    batches = load_batches(root)
    if batches is None:
        return 1
    problems = judge(root, batches)
    total = sum(len(m) for _, m in batches)
    if problems:
        print("变异批次的锚点已失效（%d 批 / %d 个变异，%d 处问题）：" % (len(batches), total, len(problems)))
        for p in problems:
            print("    %s" % p)
        print("  这些变异现在一条都跑不了：源码重构后锚点没跟着走。改 scripts/mutation/ 里的 edits。")
        return 1
    print("  变异锚点自检通过：%d 批 / %d 个变异，锚点各恰好出现一次" % (len(batches), total))
    if not selftest:
        return 0
    return self_test()


if __name__ == "__main__":
    sys.exit(main())
