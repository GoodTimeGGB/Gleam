#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 F 负例控制：输入区的水位与模型（F12）挡不挡得住。

为什么单独一批：这两样都是**只搬面板、不造数据**——水位百分比和当前模型名
本来就住在设置页深处，批次做的事是把它们抬到输入区。于是真正的风险不在
"算错"，在"线没接"：中间任何一道手把字段漏掉，界面不会报错，只会
**安静地显示一个 0%**——一个看起来"还很空"的假健康。所以每条变异都要能被改坏。

七条变异：

1. fillPct 去掉容量<=0 的保护 → 窗口容量为 0 时直接除零。
2. fillPct 不钳 100 → 上游异常时报出 150%，水位条画溢出那一栏。
3. 水位多算一轮 → 空窗口一打开就显示 25%，一个虚警。
4. Manager.Stats 恒报 0 → 源头一漏，下游全绿但界面永远空。
5. ContextView 不发 fill_pct → 同一症状的另一处源头（判据要能分别指出）。
6. 配置补丁把 fast_model 当成"没发就清空" → 切一次模型，压缩/GEO 悄悄退回主模型。
7. 配置补丁把档位表当成整段重置 → 切一次模型，两张档位表没了。

至于前端那两条判断（水位条只画回传的数、切模型必须发 model 键）：它们住在 JS 里，
套不进"改坏 Go → 编译 → 跑测试"的骨架，所以不在本批放永远存活的变异，由
`scripts/check-dom-anchors.py`（cp- 前缀的锚点反向判据）与人工走查看着。

跑法（前台，可分段；原因见 _harness.py 的 select 注释）：
    python scripts/mutation/batch-f-composer-meta.py        # 全跑
    python scripts/mutation/batch-f-composer-meta.py 1-3    # 只跑前三条
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from _harness import run_batch  # noqa: E402

GO_TEST = "go test {pkg} -run {run} -count=1 -timeout 120s"
MEM = "./internal/harness/memory"
WEBUI = "./internal/webui"


def t(pkg, run, label=None):
    return (label or f"{pkg} -run {run}", GO_TEST.format(pkg=pkg, run=run))


MUTATIONS = [
    # ---------- 水位的算法（owner：memory.fillPct） ----------
    {
        "name": "fillPct 去掉「容量<=0」保护（窗口没容量时除零）",
        "file": "internal/harness/memory/store.go",
        "edits": [("	if capacity <= 0 {\n		return 0\n	}\n", "")],
        "targets": [t(MEM, "TestFillPct_ZeroCap")],
    },
    {
        # 钳 100 这件事在**路由那一层测不出**：短期窗口是环形缓冲，轮数不会超过容量，
        # 所以 100*turns/cap 恒 ≤100。能证明它的只有直接对 fillPct 的表驱动断言
        # （{6,4,100}：万一上游异常或容量被改小，也不给前端一个撑破条的宽度）。
        "name": "fillPct 不钳 100（异常输入报出 150%）",
        "file": "internal/harness/memory/store.go",
        "edits": [(
            """	if p := 100 * turns / capacity; p < 100 {
		return p
	}
	return 100
""",
            "	return 100 * turns / capacity\n")],
        "targets": [t(MEM, "TestFillPct_ZeroCap")],
    },
    # ---------- 水位的接线（四个出口，两处最容易漏） ----------
    {
        "name": "水位多算一轮（空窗口一打开就报 25%）",
        "file": "internal/harness/memory/store.go",
        "edits": [("		FillPct:      fillPct(m.Short.Len(), m.Short.cap),",
                   "		FillPct:      fillPct(m.Short.Len()+1, m.Short.cap),")],
        "targets": [t(MEM, "TestManager_StatsFillPct")],
    },
    {
        "name": "Manager.Stats 恒报水位 0（源头一漏，下游全绿）",
        "file": "internal/harness/memory/store.go",
        "edits": [("		FillPct:      fillPct(m.Short.Len(), m.Short.cap),", "		FillPct:      0,")],
        "targets": [t(WEBUI, "TestContext_CarriesWaterLevel")],
    },
    {
        "name": "ContextView 不发 fill_pct（输入区水位条没有数据来源）",
        "file": "internal/agent/webfacade.go",
        "edits": [('		"fill_pct":         st.FillPct, // 窗口占用率：水位条只画这个数，不再自己除一遍\n', "")],
        "targets": [t(WEBUI, "TestContext_CarriesWaterLevel")],
    },
    # ---------- 就地切模型的补丁语义（只发一个键不许清掉别的） ----------
    {
        "name": "补丁把没发的 fast_model 清空（切模型后压缩悄悄退回主模型）",
        "file": "internal/config/config.go",
        "edits": [("		getStrAssign(v, \"fast_model\", &c.LLM.FastModel)",
                   "		c.LLM.FastModel, _ = v[\"fast_model\"].(string)")],
        "targets": [t(WEBUI, "TestSettings_ModelOnlyPatchIsAccepted")],
    },
    {
        "name": "补丁把档位表当成整段重置（切一次模型，两张档位都没了）",
        "file": "internal/config/config.go",
        "edits": [(
            """		if tiers, ok := v["tiers"].(map[string]any); ok {
			c.LLM.Tiers = map[string]string{}
			for name, model := range tiers {
""",
            """		c.LLM.Tiers = map[string]string{}
		if tiers, ok := v["tiers"].(map[string]any); ok {
			for name, model := range tiers {
""")],
        "targets": [t(WEBUI, "TestSettings_ModelOnlyPatchIsAccepted")],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 F：输入区水位与模型的负例控制", MUTATIONS,
                       timeout=180, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
