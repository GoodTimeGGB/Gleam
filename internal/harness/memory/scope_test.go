package memory

import (
	"path/filepath"
	"testing"
)

// 项目级记忆：同一句话在两个工作区里是两条记忆，检索也只回本工作区 + 全局的。
func TestStoreProjectScope(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "mem.json"), 64, 100)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "wsA 专属暗号 ZEBRA"

	if _, err := st.RememberScoped(secret, "", "/ws/a", nil); err != nil {
		t.Fatal(err)
	}
	// 本工作区看得见
	if len(st.SearchScoped("ZEBRA", 5, "/ws/a")) == 0 {
		t.Error("同一个工作区里应该检索得到")
	}
	// 别的工作区看不见
	if got := st.SearchScoped("ZEBRA", 5, "/ws/b"); len(got) != 0 {
		t.Errorf("不该跨工作区可见，实际 %v", got)
	}
	// scope 为空 = 不过滤（关掉项目级记忆 / 没有工作区时走这条）
	if len(st.SearchScoped("ZEBRA", 5, "")) == 0 {
		t.Error("scope 为空时不应过滤")
	}

	// 全局条目（scope 空）到处都该看得见
	if _, err := st.RememberScoped("公共暗号 KOALA", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(st.SearchScoped("KOALA", 5, "/ws/b")) == 0 {
		t.Error("全局条目在任何工作区都应可见")
	}

	// 跨 scope 不去重：同样的字面在另一个工作区应另起一条，而不是顶掉原来那条
	before := len(st.SearchScoped("ZEBRA", 5, ""))
	if _, err := st.RememberScoped(secret, "", "/ws/b", nil); err != nil {
		t.Fatal(err)
	}
	if got := len(st.SearchScoped("ZEBRA", 5, "")); got <= before {
		t.Errorf("跨工作区的同一句话应另起一条，检索总数 %d -> %d", before, got)
	}
	if len(st.SearchScoped("ZEBRA", 5, "/ws/b")) == 0 {
		t.Error("新写进 ws/b 的那条应能被 ws/b 检索到")
	}
}
