#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""文档引用完整性：所有 .md 里的 §4.6.N 引用必须指向设计文档里真实存在的小节。

**为什么要有**：指向不存在的小节是**静默失效**——读者跟着指针走到空处，
不会报错，只会得出"文档里没写"的结论；写的人也不会知道。这与 broken link 同类，
只是 Markdown 没有链接检查器管它。

**为什么归在第 1 层（格式）**：第 1 层管的是「文本层的机器可读约定」——
gofmt 管代码文本，本脚本管文档文本。两者都便宜（各只一两次进程创建）、都不需要编译。
按成本序，它必须和 gofmt 一起最早撞墙。

**为什么不做成"检查每个清单文件里有落地进度段"那种检查**：那种检查防不住真正的漂移
（表头说的与设计文档实际的不一致，要读懂语义），只会给出"已经守卫了"的错觉——
而一个不能真的失败的闸门比没有闸门更坏。**本脚本不在此列**：它判的是"指针指向的东西
在不在"，这是个可以真的失败、且失败即有缺陷的机械事实。
理由见设计文档 §4.6.23。

用法：python scripts/check-doc-refs.py [仓库根目录]
退出码：0 = 全部引用有效；1 = 有悬空引用（逐条打印文件:行号 §小节号）。
"""
import io
import os
import re
import sys

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

DESIGN = "Gleam 技术设计文档.md"
# §4.6.x 这种**泛指**写法不算引用（它说的是"这一片"，不是某一节）。
REF = re.compile(r"§(4\.6\.\d+)")
SKIP_DIRS = {".git", "node_modules", "dist", ".workbuddy-ai", ".qoder-cn", ".cursor", ".claude", ".agents", "pack"}


def main() -> int:
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    os.chdir(root)

    design_path = os.path.join(root, DESIGN)
    try:
        with io.open(design_path, "rb") as f:
            design = f.read().decode("utf-8", "replace")
    except OSError as e:
        print("读不到设计文档 %s：%s" % (DESIGN, e))
        return 1

    headings = set(re.findall(r"^### (4\.6\.\d+)", design, re.M))
    if not headings:
        # 设计文档里一个小节都没解析到，说明这个检查本身失效了——
        # 报"通过"会是最坏的结果（结构变了但检查还在假装有效）。
        print("在设计文档里没解析到任何 §4.6.N 小节，检查本身失效，请先修本脚本。")
        return 1

    bad = []
    total = 0
    files = 0
    for dirpath, dirnames, names in os.walk("."):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for n in sorted(names):
            if not n.endswith(".md"):
                continue
            p = os.path.join(dirpath, n)
            try:
                with io.open(p, "rb") as f:
                    txt = f.read().decode("utf-8", "replace")
            except OSError:
                continue
            files += 1
            for m in REF.finditer(txt):
                total += 1
                if m.group(1) not in headings:
                    line = txt[: m.start()].count("\n") + 1
                    bad.append((p, line, m.group(1)))

    if bad:
        print("文档引用不完整：%d 处悬空引用（共 %d 处引用 / %d 个文件 / 设计文档 %d 个小节）"
              % (len(bad), total, files, len(headings)))
        for p, line, sec in bad:
            print("    %s:%d  §%s 不存在" % (p, line, sec))
        print("\n要么改引用，要么补小节——别让它悬着：读者跟着走到空处，只会以为文档没写。")
        return 1

    print("文档引用完整：%d 处 §4.6.N 引用全部指向真实小节（%d 个文件 / 设计文档 %d 个小节）"
          % (total, files, len(headings)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
