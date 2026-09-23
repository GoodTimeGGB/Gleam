package growth

import "testing"

// TestRecordKeepsRuleSet 成长日志必须把规则集指纹一起落盘。
//
// 记它的理由：换了规则之后要能回答"这批历史任务是哪一版跑出来的"。
// 没有它，历史数据只是一个不可比的混合体——平均分、成功率都失去了参照意义，
// 而规则改动恰恰是最容易被忽略的那个变量（代码改了有提交记录，规则改了只有一行文本）。
func TestRecordKeepsRuleSet(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Entry{Type: "task_completed", Goal: "整理表格", Score: 88, RuleSet: "abc1234567"})

	if got := l.Recent(1); len(got) != 1 || got[0].RuleSet != "abc1234567" {
		t.Fatalf("内存里的规则集丢了：%+v", got)
	}
	// 重开一次：只留在内存里不算记录，必须能跨进程回查。
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := reopened.All()
	if len(all) != 1 {
		t.Fatalf("重开后有 %d 条记录，期望 1 条", len(all))
	}
	if all[0].RuleSet != "abc1234567" {
		t.Fatalf("落盘后规则集丢了：%+v", all[0])
	}
}

// TestRecordRuleSetOmittedForChat 对话任务没有规则集，落盘时该留空而不是写个占位——
// "空"表示这一个维度不适用，占位值会让人以为它用了某版规则。
func TestRecordRuleSetOmittedForChat(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Entry{Type: "task_completed", Goal: "你好"})
	if got := l.Recent(1)[0].RuleSet; got != "" {
		t.Fatalf("未记规则集时应当是空串，实际 %q", got)
	}
}
