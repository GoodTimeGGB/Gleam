#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""持久化写入必须走原子替换：非测试代码里不许直接 os.WriteFile。

**为什么要有**：`os.WriteFile` 的失败形态不是"这次没保存"，而是**半截文件**——
它先把文件截断，再往里写。对一份 JSON 状态来说，半截等于全丢：下次 `Open` 解析失败，
用户攒下来的技能/记忆/会话不是少了一条，是一条都不剩。而这件事只在**崩溃之后**才显形，
写的时候代码看起来完全正常。

**为什么不靠 code review 拦住**：这条规则判据是机械的（一个函数名），但**最容易在
"顺手写一下"的时候漏掉**——新加一个 store 的人不会想到去翻邻居文件怎么落盘。
机械判据配一条 exit non-zero 的命令，正是为了让它不依赖记性（AGENTS.md 铁律 3）。

**为什么归在第 1 层（格式）**：与 gofmt 同类——对代码文本的机械约定，不需要编译，
按成本序要最早撞墙。

**自我失效的防线**：如果一个 .go 文件都没扫到，说明扫描本身坏了（目录改名之类），
此时报"通过"是最坏结果，所以直接失败。

用法：python scripts/check-atomic-write.py [仓库根目录]
退出码：0 = 没有越界写法；1 = 有（逐条打印 文件:行号）。
"""
import io
import os
import re
import sys

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

# 允许直接写文件的唯一位置：原子写这一层自己。
ALLOW_PREFIX = os.path.join("internal", "atomicfile")
RAW_WRITE = re.compile(r"\bos\.WriteFile\s*\(")
SKIP_DIRS = {".git", "node_modules", "dist", ".workbuddy-ai", "pack", "bin"}


def main() -> int:
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    scan_roots = [os.path.join(root, d) for d in ("internal", "pkg", "cmd")]

    bad = []
    scanned = 0
    for base in scan_roots:
        for dirpath, dirnames, names in os.walk(base):
            dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
            rel = os.path.relpath(dirpath, root)
            if rel == ALLOW_PREFIX or rel.startswith(ALLOW_PREFIX + os.sep):
                continue
            for n in sorted(names):
                if not n.endswith(".go") or n.endswith("_test.go"):
                    continue
                p = os.path.join(dirpath, n)
                scanned += 1
                try:
                    with io.open(p, "rb") as f:
                        text = f.read().decode("utf-8", "replace")
                except OSError as e:
                    print("读不到 %s：%s" % (p, e))
                    return 1
                for i, line in enumerate(text.splitlines(), 1):
                    code = line.split("//", 1)[0]  # 注释里提它是在解释规矩，不算越界
                    if RAW_WRITE.search(code):
                        bad.append("%s:%d" % (p.replace("\\", "/"), i))

    if scanned == 0:
        print("一个 .go 文件都没扫到（%s）：检查本身失效，请先修本脚本。" % ", ".join(scan_roots))
        return 1
    if bad:
        print("持久化写入必须走 internal/atomicfile（半截 JSON = 整份状态丢失），但下列位置直接 os.WriteFile：")
        for b in bad:
            print("  " + b)
        print("  共 %d 处。改成 atomicfile.Write(path, data, perm)。" % len(bad))
        return 1
    print("  落盘写法干净：%d 个非测试 .go 文件，状态写入都走 internal/atomicfile" % scanned)
    return 0


if __name__ == "__main__":
    sys.exit(main())
