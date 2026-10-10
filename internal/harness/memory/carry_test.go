package memory

import "testing"

// 携带轮数默认是"正常态"，而且零值（没设过）也要读成正常态——
// 零值读成 0 会让新会话一个字的上下文都不带。
func TestCarryTurnsDefault(t *testing.T) {
	m := &Manager{}
	if got := m.CarryTurns(); got != CarryFull {
		t.Errorf("零值应当读成 %d，实际 %d", CarryFull, got)
	}
}

// 收紧与松开都要能设进去，并钳在 [CarryTight, CarryFull] 之内：
// 越界的值只可能来自将来的某个调用点写错，钳住它比让它生效安全。
func TestSetCarryTurnsClampsAndReportsChange(t *testing.T) {
	m := &Manager{}
	if !m.SetCarryTurns(CarryTight) {
		t.Error("从正常态收到收紧态应当报告变化")
	}
	if got := m.CarryTurns(); got != CarryTight {
		t.Errorf("应当是 %d，实际 %d", CarryTight, got)
	}
	if m.SetCarryTurns(CarryTight) {
		t.Error("已经是这个值了，不该报告变化（水位附近每轮都会调用它）")
	}
	if !m.SetCarryTurns(CarryFull) {
		t.Error("松开也应当报告变化")
	}
	if got := m.CarryTurns(); got != CarryFull {
		t.Errorf("应当回到 %d，实际 %d", CarryFull, got)
	}

	m.SetCarryTurns(0)
	if got := m.CarryTurns(); got != CarryTight {
		t.Errorf("0 应当被钳到 %d，实际 %d", CarryTight, got)
	}
	m.SetCarryTurns(999)
	if got := m.CarryTurns(); got != CarryFull {
		t.Errorf("超大值应当被钳到 %d，实际 %d", CarryFull, got)
	}
}

// 开新对话要回到正常携带：上一个会话是因为它自己攒满了才收紧的，那件事不该跟过来。
func TestResetConversationRelaxesCarry(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, 20, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	m.SetCarryTurns(CarryTight)
	m.ResetConversation()
	if got := m.CarryTurns(); got != CarryFull {
		t.Errorf("新对话应当回到 %d，实际 %d", CarryFull, got)
	}
}
