#!/usr/bin/env python3
"""变异测试的公共骨架。

为什么要有它：变异脚本以前每批都写在临时目录里、跑完即删，于是每批都要重新
踩同一批坑——被中断后源码留在变异状态、锚点匹配到两处、Windows 上文本模式把
换行翻成 CRLF、把环境自身的报错当成"变异被捕获"。这里把那些坑一次性处理掉，
每批只需要声明"改哪一处、跑哪个命令"。

用法见 batch-*.py。每个变异是一个 dict：
    name    变异名（会打印出来）
    file    相对仓库根的文件路径
    edits   [(old, new), ...] —— 每处 old 在文件里必须**恰好出现一次**
    targets [(标签, 命令), ...] —— 任一命令失败即视为"被捕获"

判定：命令返回非零 = 被捕获。所有 target 都通过 = 变异存活（**这是问题**）。
"""

from __future__ import annotations

import atexit
import os
import signal
import subprocess
import sys

# 骨架自己也要印中文和 ✓，所以先修标准流：Windows 上 Python 按本地代码页（本机 cp936）
# 写 stdout。中文变乱码还算轻的——`✓` 不在 GBK 里，会直接抛 UnicodeEncodeError，
# 而它偏偏崩在"变异已被捕获"那一行之后：文件已还原（无害），但整批剩下的没跑，
# 看起来像"跑到一半自己没了"。`scripts/check-py-utf8.py` 现在连本目录一起罩。
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import _utf8  # noqa: E402,F401

# 环境自身的报错（不是我们的测试在报错）。本机 Go 工具链偶发地把标准库报成
# "not in std"，那是环境噪声；不识别它就会把噪声当成"变异被捕获"，
# 于是变异验证给出一个假的满分。
ENV_MARKERS = (
    "is not in std",
    "package internal/sysinfo",
    "package strings is not in std",
    "SIGTERM",
    "signal: terminated",
)

# 变异导致编译失败时的标志。这类"捕获"是假的——它证明的是改法写坏了，
# 不是断言有效。
BUILD_FAIL_MARKERS = (
    "[build failed]",
    "syntax error",
    "missing return",
    "declared and not used",
    "undefined:",
    "cannot use",
)

MAX_ENV_RETRIES = 3


class Harness:
    def __init__(self, root: str):
        self.root = os.path.abspath(root)
        self._restore: list[tuple[str, bytes]] = []
        self._interrupted = False
        atexit.register(self.restore_all)
        for sig in (signal.SIGINT, signal.SIGTERM):
            signal.signal(sig, self._on_signal)

    # ---------- 中断处理 ----------
    def _on_signal(self, signum, _frame):
        # 被中断时必须先把源码还原再退出：留下一个变异过的仓库，
        # 下一个人会在完全莫名其妙的地方看到失败。
        self._interrupted = True
        self.restore_all()
        sys.exit(130)

    def restore_all(self):
        while self._restore:
            path, original = self._restore.pop()
            with open(path, "wb") as f:
                f.write(original)
            self._drop_backup(path)

    # ---------- 磁盘备份 ----------
    #
    # 内存里的备份救不了"进程被强杀"：信号处理器在，但被 SIGKILL 或被沙箱
    # 直接掐掉时它不会执行，仓库就停在一个改了一半的状态上。下次启动的预检
    # 虽然能发现，却只能让人手工修。所以把备份同时写到磁盘：
    # 下次启动时若发现备份还在，说明上次没跑干净，直接用它还原。
    def _backup_path(self, path: str) -> str:
        return path + ".mutbak"

    def _write_backup(self, path: str, data: bytes):
        with open(self._backup_path(path), "wb") as f:
            f.write(data)

    def _drop_backup(self, path: str):
        try:
            os.remove(self._backup_path(path))
        except OSError:
            pass

    def recover_from_backup(self, files: list[str]) -> list[str]:
        """用磁盘备份还原上次没跑干净的改动。返回被还原的文件列表。"""
        recovered = []
        for rel in files:
            path = self.path_of(rel)
            bak = self._backup_path(path)
            if not os.path.exists(bak):
                continue
            with open(bak, "rb") as f:
                data = f.read()
            with open(path, "wb") as f:
                f.write(data)
            os.remove(bak)
            recovered.append(rel)
        return recovered

    # ---------- 读写（一律二进制） ----------
    def path_of(self, rel: str) -> str:
        return os.path.join(self.root, rel.replace("/", os.sep))

    def read(self, rel: str) -> bytes:
        with open(self.path_of(rel), "rb") as f:
            return f.read()

    def write(self, rel: str, data: bytes):
        with open(self.path_of(rel), "wb") as f:
            f.write(data)

    def snapshot(self, rel: str):
        path = self.path_of(rel)
        if not any(p == path for p, _ in self._restore):
            data = self.read(rel)
            self._restore.append((path, data))
            self._write_backup(path, data)

    # ---------- 变异 ----------
    def apply(self, mut: dict) -> bool:
        """应用一个变异。锚点不唯一/不存在就返回 False（预检失败，不跑命令）。"""
        rel = mut["file"]
        self.snapshot(rel)
        text = self.read(rel).decode("utf-8")
        for old, new in mut["edits"]:
            n = text.count(old)
            if n != 1:
                # 锚点必须唯一。匹配到 0 处说明代码变了（脚本过期）；
                # 匹配到多处说明这次改的可能是别的地方——两种都必须先修脚本，
                # 而不是"随便挑一处改"。
                print(f"    [预检失败] 锚点在 {rel} 中出现 {n} 次（必须恰好 1 次）：{old[:60]!r}")
                self.restore_all()
                return False
            text = text.replace(old, new)
        self.write(rel, text.encode("utf-8"))
        return True

    def residue(self, mut: dict) -> bool:
        """还原后确认锚点回到恰好 1 次——否则说明源码没还原干净。"""
        self.restore_all()
        text = self.read(mut["file"]).decode("utf-8")
        for old, _ in mut["edits"]:
            if text.count(old) != 1:
                return False
        return True

    # ---------- 跑命令 ----------
    def run(self, cmd: str, timeout: int) -> tuple[bool, str]:
        """返回 (是否通过, 输出)。环境错误会自动重试。"""
        for attempt in range(1, MAX_ENV_RETRIES + 1):
            try:
                p = subprocess.run(
                    cmd, shell=True, cwd=self.root, timeout=timeout,
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                )
                out = p.stdout.decode("utf-8", "replace")
                if p.returncode == 0:
                    return True, out
                if any(m in out for m in ENV_MARKERS) and attempt < MAX_ENV_RETRIES:
                    print(f"    [环境错误，重试 {attempt}/{MAX_ENV_RETRIES - 1}] {cmd}")
                    continue
                return False, out
            except subprocess.TimeoutExpired as e:
                out = (e.stdout or b"").decode("utf-8", "replace")
                # 超时按"捕获"算：变异若把代码变成死等，超时正是它该有的症状。
                if attempt < MAX_ENV_RETRIES and any(m in out for m in ENV_MARKERS):
                    continue
                return False, out + "\n[超时]"
        return False, ""


def select(mutations: list[dict], spec: str) -> list[dict]:
    """按 "a-b"（1 起、含两端）或名字子串挑选变异。

    为什么要能分段跑：整批跑十几分钟，而前台执行的窗口有限；更重要的是
    **后台执行会被沙箱拦在 Go 构建缓存上**，而一次被拦下的命令看起来就像
    "测试失败"——于是变异被记成"已捕获"。那是比跑不动更坏的结果：
    它会给出一个假的满分。所以宁可分段、每段都在前台跑完。
    """
    if not spec:
        return mutations
    if "-" in spec and all(p.isdigit() for p in spec.split("-", 1)):
        a, b = (int(x) for x in spec.split("-", 1))
        return [m for i, m in enumerate(mutations, 1) if a <= i <= b]
    return [m for m in mutations if spec in m["name"]]


def preflight(h: Harness, mutations: list[dict]) -> list[str]:
    """启动时的完整性预检：锚点各恰好出现一次，且目标命令不含 `|`。

    为什么非做不可：脚本被强杀（沙箱拦截、超时、Ctrl-C）时可能留下一个已经改过、
    但还没还原的文件。此时后续每一处锚点都会失配，而更坏的情况是**锚点仍然匹配**——
    于是变异被"应用"到一个已经被改过的文件上，跑出来的结果毫无意义。
    更危险的是：一次被沙箱拦下的命令看起来就是"测试失败"，于是变异被记成"已捕获"。

    `|` 同理：Windows 上 shell=True 走 cmd.exe，而 cmd 不认单引号，
    `go test -run 'A|B'` 压根没跑起来——"命令失败"会被记成"断言挡住了"。
    这条口径只写在 README 里迟早被忘，所以在**跑之前**就拒绝：宁可不起手。

    所以先确认"起点是干净的"，再谈结果。发现异常就直接退出，不做任何修改。
    """
    problems = []
    for mut in mutations:
        for _, cmd in mut["targets"]:
            if "|" in cmd:
                problems.append(f"{mut['name']}：目标命令含 `|`（cmd.exe 会当管道，命令不会跑起来）：{cmd}")
        text = h.read(mut["file"]).decode("utf-8")
        for old, _ in mut["edits"]:
            n = text.count(old)
            if n != 1:
                problems.append(f"{mut['name']}：{mut['file']} 中锚点出现 {n} 次（应 1 次）")
    return problems


def run_batch(title: str, mutations: list[dict], timeout: int = 240, spec: str = ""):
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    h = Harness(root)

    print(f"===== {title} =====")
    print(f"仓库：{h.root}")

    # 先看有没有上次没跑干净留下的备份：有就直接还原（比让人手工修可靠）。
    files = sorted({m["file"] for m in mutations})
    recovered = h.recover_from_backup(files)
    if recovered:
        print("发现上次未跑干净的改动，已从磁盘备份还原：")
        for f in recovered:
            print(f"  - {f}")

    # 全量预检（**不只是本次筛出来的那几条**）：仓库必须处于干净状态。
    # 只看本次要跑的那几条，会漏掉"上一次跑留下的半截变异"。
    problems = preflight(h, mutations)
    if problems:
        print("===== 预检失败：仓库不干净或脚本已过期，拒绝运行 =====")
        for p in problems:
            print(f"  - {p}")
        print("\n先还原源码（或更新脚本里的锚点），再重跑。")
        return 2
    print(f"预检通过：{len(mutations)} 个变异的锚点各出现一次，仓库处于干净状态。")

    mutations = select(mutations, spec)
    if spec:
        print(f"筛选：{spec} → {len(mutations)} 个变异")
    print(f"本次变异数：{len(mutations)}\n")

    caught, survived, skipped, invalid = [], [], [], []
    for i, mut in enumerate(mutations, 1):
        print(f"[{i}/{len(mutations)}] {mut['name']}")
        if not h.apply(mut):
            skipped.append(mut["name"])
            continue
        try:
            failed_targets = []
            for label, cmd in mut["targets"]:
                ok, out = h.run(cmd, timeout)
                if not ok:
                    # 第一处报错就够了：后面的目标命令再跑一遍既慢，也不会改变结论。
                    failed_targets.append((label, out))
                    break
            if failed_targets:
                label, out = failed_targets[0]
                tail = [l for l in out.strip().splitlines() if l.strip()][-3:]
                if any(m in out for m in BUILD_FAIL_MARKERS):
                    # 不能编译的变异什么也证明不了：它"被捕获"的理由是语法错误，
                    # 而不是断言真的挡住了行为。必须当成无效变异单独列出来，
                    # 否则它会悄悄把捕获率抬上去。
                    print("    无效 ✗（变异没能编译——它证明不了任何事，请修正改法）")
                    for l in tail:
                        print(f"      | {l[:150]}")
                    invalid.append(mut["name"])
                else:
                    print(f"    捕获 ✓（{label} 报错）")
                    for l in tail:
                        print(f"      | {l[:150]}")
                    caught.append(mut["name"])
            else:
                print("    **存活 ✗** —— 所有目标命令都通过了，这个变异没有被任何断言挡住")
                survived.append(mut["name"])
        finally:
            if not h.residue(mut):
                print(f"    [严重] {mut['file']} 未能还原干净，请手动检查")
                sys.exit(2)

    print(f"\n===== 结果：{len(caught)}/{len(mutations)} 捕获 =====")
    if survived:
        print("存活（必须补断言）：")
        for n in survived:
            print(f"  - {n}")
    if invalid:
        print("无效（变异没能编译，证明不了任何事，必须修正改法）：")
        for n in invalid:
            print(f"  - {n}")
    if skipped:
        print("跳过（锚点预检失败，脚本需更新）：")
        for n in skipped:
            print(f"  - {n}")
    if not survived and not skipped and not invalid:
        print("全部捕获，源码已还原。")
    return 1 if (survived or skipped or invalid) else 0
