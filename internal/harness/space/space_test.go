package space

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	s, err := Open(dataDir, dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dataDir
}

// TestSpaceIDTraversalCannotEscapeStore 复刻 2026-09-24 接口走查实测：
// 用 Windows 分隔符 / 保留名把 id 拼成 spaces 目录之外的路径去删、去读。
// 存储层必须一律 ErrInvalidID，且目录外的文件毫发无损。
func TestSpaceIDTraversalCannotEscapeStore(t *testing.T) {
	s, dataDir := openStore(t)
	mark := filepath.Join(dataDir, "mark.json")
	if err := os.WriteFile(mark, []byte(`{"id":"default","name":"外部文件"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// state 是激活态保留文件，不能被当成空间删掉
	stateJSON := s.statePath()

	for _, bad := range []string{
		".." + string(filepath.Separator) + "mark", // ..\mark
		"../mark", // ../mark
		"..\\..\\etc\\passwd",
		"state",       // 保留名（会命中 spaces/state.json）
		"state.json",  // 保留名的另一写法
		"default ",    // 尾空格
		"sp_zzzzzzzz", // 形态不符
	} {
		if err := s.Delete(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Delete(%q) = %v，应为 ErrInvalidID", bad, err)
		}
		if _, err := s.Get(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Get(%q) = %v，应为 ErrInvalidID", bad, err)
		}
		if _, err := s.Rename(bad, "x"); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Rename(%q) = %v，应为 ErrInvalidID", bad, err)
		}
		if _, err := s.SetPath(bad, ""); !errors.Is(err, ErrInvalidID) {
			t.Errorf("SetPath(%q) = %v，应为 ErrInvalidID", bad, err)
		}
		if err := s.SetActive(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("SetActive(%q) = %v，应为 ErrInvalidID", bad, err)
		}
	}
	if _, err := os.Stat(mark); err != nil {
		t.Errorf("目录外文件被删除/移动了：%v", err)
	}
	if _, err := os.Stat(stateJSON); err != nil {
		t.Errorf("保留文件 state.json 被删除了：%v", err)
	}
}

// TestSpaceNotFoundVsInvalid 合法形态但不存在的 id 报 ErrNotFound（供处理器回 404）。
func TestSpaceNotFoundVsInvalid(t *testing.T) {
	s, _ := openStore(t)
	if _, err := s.Get("sp_000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(不存在) = %v，应为 ErrNotFound", err)
	}
	if err := s.SetActive("sp_000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetActive(不存在) = %v，应为 ErrNotFound", err)
	}
}

// TestSpaceNormalRoundTrip 正常路径仍可读写，且落盘是原子的（不留 .tmp 半截文件）。
func TestSpaceNormalRoundTrip(t *testing.T) {
	s, dataDir := openStore(t)
	sp, err := s.Create("测试空间", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sp.ID, "sp_") {
		t.Fatalf("新空间 id 形态异常: %q", sp.ID)
	}
	got, err := s.Get(sp.ID)
	if err != nil || got.Name != "测试空间" {
		t.Fatalf("Get = %v, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dataDir, "spaces"))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}
