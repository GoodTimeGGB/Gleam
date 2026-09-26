#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""闸门脚本的 stdout 自修：**只改编码，不改内容。**

**为什么要有**：Windows 上 Python 的 stdout 按**本地代码页**写（本机是 cp936/GBK），
于是 `check-doc-budget.py` 打印的中文在管道、日志和 UTF-8 终端里全是乱码。
一条读不懂的报错等于没有报错——判据的输出不能依赖每个人的启动方式。

**为什么不靠调用方加 `-X utf8`**：`AGENTS.md` 的命令表写的是 `scripts/check-*.py`，
人和 agent 都会直接敲；记住加参数的那次恰好是别人在看的最后一次。所以编码在脚本自己手里定。

**边界**：只 reconfigure 标准流。业务文本一个字不动——这不是转码工具，
真要把日志转码就用 `iconv`。非 Windows、或流已经被 reconfigure 过，都是空操作。
`errors="backslashreplace"` 是兜底：宁可看见 `\\uXXXX` 也不要在打印时抛异常，
那会把一条"告警"变成"闸门自己崩了"。

用法：在 `check-*.py` 里 `import _utf8  # noqa: F401`（同目录，无需处理 sys.path）。
"""
import sys

for _name in ("stdout", "stderr"):
    _stream = getattr(sys, _name, None)
    if _stream is not None and hasattr(_stream, "reconfigure"):
        try:
            _stream.reconfigure(encoding="utf-8", errors="backslashreplace")
        except (ValueError, OSError):
            # 流已被 detach 或不是文件流（例如被测试替身换掉）——不因此让判据失败。
            pass
