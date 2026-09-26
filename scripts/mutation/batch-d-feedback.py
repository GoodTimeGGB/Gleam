#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批次 D 负例控制：反馈与建议（F10）那几条判断挡不挡得住。

为什么单独一批：这一批改的全是**判断**而不是格式——"发出去之前该不该脱一次敏"、
"投不出去该说什么状态"、"这张图到底是不是图"。格式错了编译就拦，判断错了什么都不会
发生：界面照常回 201，测试照常绿，损失要等到真有人读那条反馈时才显形。

七条变异，每条都对应一次"如果当年写歪了会怎样"：

1. 投递前不脱敏 → 用户粘在描述里的工作区路径与密钥整份发出去。
2. 现场字段带上完整接入 URL → 某些厂商把 token 放在路径段里，等于把凭证发出去。
3. 投递失败静默写成 local_only → "没配远端"与"没送到"分不开，用户以为送达了。
4. 投递结果不回写归档 → 刷新之后红点消失，失败这件事只在那一次响应里存在过。
5. 已送达还重复投 → 表里多出一条重复行，"这条收没收到"从此说不清。
6. 图片只认扩展名不认文件头 → 改名成 .png 的脚本被当图片存下并投递（两处靶子）。
7. 空描述被放过 → 一条没有文字的反馈在列表里看起来像"提交了却没生效"。

注：**"先落本地归档、再谈投递"这个顺序刻意不做负例控制。** 已实测：把 `Save`
整块挪到 `deliverFeedback` 之后，`go test ./internal/webui -run TestFeedback` 全绿
（本地那份照样在，只是写得晚了一步）。它真正挡的是"归档还没落住就断网/进程没了"，
而那需要一个注入点让 Save 之前先中断——现有断言只查"文件在不在"，查不到时序。
硬凑一条测不出的变异只会给出"已守卫"的错觉，比不测更坏（同批次 B 里"归档先于终态广播"那条的处理）。

跑法（前台，可分段；原因见 _harness.py 的 select 注释）：
    python scripts/mutation/batch-d-feedback.py        # 全跑
    python scripts/mutation/batch-d-feedback.py 1-3    # 只跑前三条
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from _harness import run_batch  # noqa: E402

GO_TEST = "go test {pkg} -run {run} -count=1 -timeout 120s"
WEBUI = "./internal/webui"
STORE = "./internal/harness/feedback"


def t(pkg, run, label=None):
    return (label or f"{pkg} -run {run}", GO_TEST.format(pkg=pkg, run=run))


MUTATIONS = [
    {
        "name": "投递前不脱敏（本机已知机密原样发出）",
        "file": "internal/webui/feedback_handlers.go",
        "edits": [("out, redacted := feedback.Redact(f, s.feedbackSecrets())",
                   "out, redacted := feedback.Redact(f, []string{})")],
        "targets": [t(WEBUI, "TestFeedbackSubmit_DeliversRedactedCopy")],
    },
    {
        "name": "现场字段带上完整接入 URL（白名单失守）",
        "file": "internal/webui/feedback_handlers.go",
        "edits": [("Model:      strings.TrimSpace(cfg.LLM.Model),",
                   'Model:      strings.TrimSpace(cfg.LLM.Model + " @" + cfg.LLM.BaseURL),')],
        "targets": [t(WEBUI, "TestFeedbackSubmit_ContextIsServerSideAndHostOnly")],
    },
    {
        "name": "投递失败静默写成 local_only（没配与没送成分不开）",
        "file": "internal/webui/feedback_handlers.go",
        "edits": [("f.Delivery = types.FeedbackFailed", "f.Delivery = types.FeedbackLocalOnly")],
        "targets": [t(WEBUI, "TestFeedbackSubmit_DeliveryFailureStaysLocalAndVisible")],
    },
    {
        "name": "投递结果不回写归档（失败状态只活一次响应）",
        "file": "internal/webui/feedback_handlers.go",
        "edits": [("""	if err := s.feedback.Save(f); err != nil {
		fmt.Fprintf(os.Stderr, "[gleam] 反馈 %s 的投递状态没写回去：%v\\n", f.ID, err)
	}""", "\t_ = f")],
        "targets": [t(WEBUI, "TestFeedbackSubmit_DeliveryFailureStaysLocalAndVisible")],
    },
    {
        "name": "已送达还重复投（表里多出一条重复行）",
        "file": "internal/webui/feedback_handlers.go",
        "edits": [("if sink == nil || f.Delivery == types.FeedbackSent {", "if sink == nil {")],
        "targets": [t(WEBUI, "TestFeedbackResend_DoesNotRepeatSentRows")],
    },
    {
        "name": "图片不校验文件头（什么字节都当 PNG 收）",
        "file": "internal/harness/feedback/store.go",
        "edits": [("""	case "image/webp":
		return ".webp", true
	}
	return "", false""", """	case "image/webp":
		return ".webp", true
	}
	return ".png", true""")],
        "targets": [t(STORE, "TestAttachment_SniffsRealType"),
                    t(WEBUI, "TestFeedbackSubmit_FakeImageRollsBack")],
    },
    {
        "name": "空描述被放过（一条没字的反馈照样落盘）",
        "file": "internal/webui/feedback_handlers.go",
        "edits": [('	if text == "" {\n\t\twriteErr(w, 400, "请先写一句描述',
                   '	if text == "\\x00绝不可能等于描述" {\n\t\twriteErr(w, 400, "请先写一句描述')],
        "targets": [t(WEBUI, "TestFeedbackSubmit_RejectsBadInput")],
    },
]


if __name__ == "__main__":
    sys.exit(run_batch("批次 D：反馈与建议（F10）判断的负例控制", MUTATIONS,
                       timeout=300, spec=sys.argv[1] if len(sys.argv) > 1 else ""))
