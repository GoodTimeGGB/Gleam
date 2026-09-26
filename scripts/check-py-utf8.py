#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""闸门脚本自己会不会把中文打成乱码：会往外印非 ASCII 的 `check-*.py` 必须自修标准流。

**为什么要有**：Windows 上 Python 的 stdout 按**本地代码页**写（本机 cp936）。
一条用 `python scripts/check-x.py` 直接敲出来的中文告警，在终端与日志里是一串乱码——
而 `verify.sh` 的判据是**给人读的那一行**，读不懂的告警等于没有告警。
这不是假设：`check-doc-budget.py` 就带着这个状态存在了很久，直到有人把它的输出抓成字节看了一眼。

**为什么不靠 code review 拦住**：新写一个检查器时，"要不要 reconfigure stdout" 看起来
像调用方的事（`verify.sh` 里加 `-X utf8` 就完事了），而 `AGENTS.md` 命令表写的是
`scripts/check-*.py`——人和 agent 都会直接敲，记住加参数的那一次恰好是别人在看的最后一次。
所以这条约定必须自己站在文件里，而站在文件里就会有人忘：忘一次要等到有人读不懂报错才发现。

**怎么改才通过**：在文件顶部的 import 块里加一行 `import _utf8  # noqa: F401`（同目录模块，
无需处理 sys.path）。它只 reconfigure 标准流的编码，不改业务文本。
真要印纯 ASCII 的检查器可以不加——判据只看「有没有非 ASCII 的字符串字面量」。

**自我失效的防线**：`scripts/_utf8.py` 不见了就报错退出（判据依赖的东西丢了，
不能点头说"通过"）。

用法：python scripts/check-py-utf8.py [--self-test] [仓库根目录]
退出码：0 = 全部合规（`--self-test` 时：两份构造文本各按预期）；1 = 有漏的，或判据本身失效。
"""
import ast
import io
import os
import subprocess
import sys

import _utf8  # noqa: F401  # 本文件自己也要印中文，先修好再说话

GLOB_PREFIX = "check-"
GLOB_SUFFIX = ".py"
HELPER = os.path.join("scripts", "_utf8.py")
IMPORT_MARK = "import _utf8"


def has_non_ascii_literal(source):
    """源码里是否存在**会被打印**的非 ASCII 文本：字符串字面量（含 f-string 的静态段）。

    注释与 docstring 不算——它们不经过 stdout。
    """
    tree = ast.parse(source)
    skip = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Module, ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            doc = ast.get_docstring(node, clean=False)
            if doc and getattr(node, "body", None):
                first = node.body[0]
                if isinstance(first, ast.Expr) and isinstance(first.value, ast.Constant):
                    skip.add(id(first.value))
    for node in ast.walk(tree):
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            if id(node) in skip:
                continue
            if any(ord(ch) > 127 for ch in node.value):
                return True
    return False


def check_source(name, source):
    """返回不合规的原因；合规返回 None。"""
    if IMPORT_MARK in source:
        return None
    try:
        prints_cjk = has_non_ascii_literal(source)
    except SyntaxError as exc:
        return "%s 语法不过（%s）：无法判断它印什么" % (name, exc.msg)
    if not prints_cjk:
        return None
    return ("%s 会往 stdout 印非 ASCII 文本却没有 import _utf8："
            "Windows 上中文会变乱码，一条读不懂的告警等于没有告警" % name)


def candidates(root):
    d = os.path.join(root, "scripts")
    if not os.path.isdir(d):
        return None
    return sorted(os.path.join(d, f) for f in os.listdir(d)
                  if f.startswith(GLOB_PREFIX) and f.endswith(GLOB_SUFFIX))


def self_test(root):
    """两份构造文本：漏了要拦，补上了要放行。"""
    bad_src = 'import sys\n\nprint("接口清单与实现不一致")\n'
    good_src = 'import sys\n\nimport _utf8  # noqa: F401\n\nprint("接口清单与实现一致")\n'
    ok = True
    reason = check_source("check-x.py", bad_src)
    if reason:
        print("  拦住：只印中文却没自修（%s）" % reason.split("：")[0])
    else:
        print("  未拦住：只印中文却没 import _utf8 的文本被放过了——判据失效")
        ok = False
    if check_source("check-y.py", good_src) is None:
        print("  放行：加了 import _utf8 的文本（不误伤）")
    else:
        print("  误伤：已经 import _utf8 的文本仍被拦下——判据失效")
        ok = False
    # docstring 里全是中文、但只印 ASCII 的检查器不该被拖进来
    ascii_src = '"""闸门脚本：中文说明很长很长。"""\nprint("ok")\n'
    if check_source("check-z.py", ascii_src) is None:
        print("  放行：docstring 写中文而输出纯 ASCII（判据只管真会被印出来的字）")
    else:
        print("  误伤：只把中文写在 docstring 里的文本被拦下——判据管太宽")
        ok = False
    # 拿真文件跑一遍，确认负例不是只活在构造文本里
    paths = candidates(root) or []
    bad = []
    for path in paths:
        with io.open(path, encoding="utf-8", errors="replace") as fh:
            reason = check_source(os.path.basename(path), fh.read())
        if reason:
            bad.append(reason)
    if bad:
        print("  真实文件里有不合规的（本例应干净，除非你刚改了脚本）：")
        for r in bad:
            print("    " + r)
        ok = False
    else:
        print("  真实文件全部合规：%d 个 check-*.py" % len(paths))
    # 子进程里测一条真实命令的字节编码：判据说"已自修"，输出就得真是 UTF-8
    if paths:
        probe = paths[0]
        proc = subprocess.run([sys.executable, probe, "."], stdout=subprocess.PIPE, cwd=root)
        try:
            proc.stdout.decode("utf-8")
            print("  实测编码：%s 的输出是 UTF-8 字节" % os.path.basename(probe))
        except UnicodeDecodeError:
            print("  实测编码：%s 的输出不是 UTF-8——import _utf8 没生效" % os.path.basename(probe))
            ok = False
    return 0 if ok else 1


def main() -> int:
    args = [a for a in sys.argv[1:]]
    do_self = "--self-test" in args
    args = [a for a in args if a != "--self-test"]
    root = args[0] if args else "."

    if not os.path.isfile(os.path.join(root, HELPER)):
        print("找不到 %s：这道判据依赖它，判据失效，请先修" % HELPER, file=sys.stderr)
        return 1

    if do_self:
        return self_test(root)

    paths = candidates(root)
    if paths is None:
        print("找不到 scripts/ 目录：无法检查（宁可红着，也不给「已通过」的错觉）", file=sys.stderr)
        return 1
    bad = []
    for path in paths:
        with io.open(path, encoding="utf-8", errors="replace") as fh:
            reason = check_source(os.path.basename(path), fh.read())
        if reason:
            bad.append(reason)
    if bad:
        print("%d 个闸门脚本会把中文打成乱码：" % len(bad), file=sys.stderr)
        for r in bad:
            print("  " + r, file=sys.stderr)
        return 1
    print("闸门脚本输出编码一致：%d 个 check-*.py 都不依赖调用方加 -X utf8" % len(paths))
    return 0


if __name__ == "__main__":
    sys.exit(main())
