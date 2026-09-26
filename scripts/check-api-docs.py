#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""接口清单与实现一致：README 端点表 == `internal/webui` 注册的路由集合（双向）。

**为什么要有**：README 是「用户可见能力清单」的 owner。接口已经接上、前端也点得到，
但清单上没有——那么对下一个读仓库的人（含 agent）这件事**不存在**：他会重新提一遍，
或者干脆不知道自己能用。反过来更坏：清单上写了一个代码里没注册的端点，
读者照着调会得到 404，而 404 是一个明确、自信的答复（同一类静默失效）。
所以两个方向都判：`缺` 与 `多` 各列一段。

**为什么只判路径、不判方法**：方法写错，前端一调就 502/405，当场暴露；
路径写错没人会发现——表格里 `POST /api/goasl` 长得跟真的一样。
把闸门放在"不会被自动发现的那一侧"，才不浪费每次进程创建的钱。

用法：python scripts/check-api-docs.py [仓库根目录]
退出码：0 = 一致；1 = 有缺或有悬空条目（逐条打印）。
"""
import io
import os
import re
import sys

import _utf8  # noqa: F401  # Windows 下 stdout 默认按 GBK 写，中文会变乱码

WEBUI = os.path.join("internal", "webui")
DOC = "README.md"
# Go 1.22 路由声明：mux.HandleFunc("GET /api/goals/{id}", s.handleGoalGet)
ROUTE = re.compile(r'HandleFunc\("[A-Z]+ (/api/[^"]*)"')
# 文档里只认反引号包起来的片段中的路径，避免正文口语提到的 "/api/…" 被当成条目
SPAN = re.compile(r"`([^`]*)`")
PATH = re.compile(r"(/api/[A-Za-z0-9_/{}.\-]*)")


def code_routes(root):
    """注册在 webui 上的 /api 路径。跳过 _test.go：测试里的假路由不是用户能调的东西。"""
    paths = set()
    for name in sorted(os.listdir(os.path.join(root, WEBUI))):
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        with io.open(os.path.join(root, WEBUI, name), "rb") as f:
            src = f.read().decode("utf-8", "replace")
        paths.update(ROUTE.findall(src))
    return paths


def doc_paths(text):
    paths = set()
    for span in SPAN.findall(text):
        m = PATH.search(span)
        if m:
            paths.add(m.group(1))
    return paths


def main() -> int:
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    os.chdir(root)

    try:
        with io.open(DOC, "rb") as f:
            readme = f.read().decode("utf-8", "replace")
    except OSError as e:
        print("读不到能力清单 %s：%s" % (DOC, e))
        return 1

    registered = code_routes(root)
    if not registered:
        # 一条都没解析到 = 检查本身失效（路由声明换了写法）。报"通过"是最坏结果：
        # 它会让所有人以为这条规则有闸门。
        print("在 %s 里没解析到任何 /api 路由，检查本身失效，请先修本脚本。" % WEBUI)
        return 1

    documented = doc_paths(readme)
    missing = sorted(registered - documented)
    phantom = sorted(documented - registered)

    if missing or phantom:
        print("接口清单与实现不一致（代码 %d 条 / 清单 %d 条）" % (len(registered), len(documented)))
        for p in missing:
            print("    缺  %s —— 已注册但清单没写，下一个读仓库的人不知道它存在" % p)
        for p in phantom:
            print("    多  %s —— 清单写了但没注册，照着调只会拿到 404" % p)
        return 1

    print("接口清单与实现一致：%d 条 /api 路由全部在册" % len(registered))
    return 0


if __name__ == "__main__":
    sys.exit(main())
