package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChromeAppArgsIncludesProfileAndAppURL(t *testing.T) {
	args := chromeAppArgs("http://127.0.0.1:8787/", `C:\Users\x\.gleam\browser-profile`, "Gleam · 微光")
	joined := ""
	for _, a := range args {
		joined += a + "\n"
	}
	for _, want := range []string{
		"--app=http://127.0.0.1:8787/",
		"--user-data-dir=C:\\Users\\x\\.gleam\\browser-profile",
		"--app-name=Gleam · 微光",
		"--window-name=Gleam · 微光",
		"--no-first-run",
	} {
		if !containsLine(args, want) {
			t.Errorf("chromeAppArgs missing %q; got %v", want, args)
		}
	}
	_ = joined
}

func containsLine(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestShouldFallbackAfterAppLaunch(t *testing.T) {
	cases := []struct {
		n    int
		ok   bool
		want bool
	}{
		{0, false, true},
		{0, true, true},
		{1, false, true},
		{1, true, false},
		{2, true, false},
	}
	for _, c := range cases {
		if got := shouldFallbackAfterAppLaunch(c.n, c.ok); got != c.want {
			t.Errorf("shouldFallbackAfterAppLaunch(%d,%v)=%v want %v", c.n, c.ok, got, c.want)
		}
	}
}

func TestPreferEdgeOverChrome(t *testing.T) {
	in := []string{
		`C:\LAD\Google\Chrome\Application\chrome.exe`,
		`C:\LAD\Microsoft\Edge\Application\msedge.exe`,
		`C:\PF\Google\Chrome\Application\chrome.exe`,
		`C:\PF\Microsoft\Edge\Application\msedge.exe`,
	}
	out := preferEdgeOverChrome(in)
	if len(out) != 4 {
		t.Fatalf("len=%d", len(out))
	}
	if filepath.Base(out[0]) != "msedge.exe" || filepath.Base(out[1]) != "msedge.exe" {
		t.Errorf("Edge should lead: %v", out)
	}
	if filepath.Base(out[2]) != "chrome.exe" || filepath.Base(out[3]) != "chrome.exe" {
		t.Errorf("Chrome should follow Edge: %v", out)
	}
}

func TestProfileLockHelpers(t *testing.T) {
	dir := t.TempDir()
	if anyProfileLockPresent(dir) {
		t.Fatal("empty dir should have no lock")
	}
	lock := filepath.Join(dir, "SingletonLock")
	if err := os.WriteFile(lock, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !anyProfileLockPresent(dir) {
		t.Fatal("SingletonLock should be detected")
	}
	removeStaleProfileLocks(dir)
	if anyProfileLockPresent(dir) {
		t.Fatal("locks should be removed")
	}
	paths := profileLockPaths(dir)
	if len(paths) != 3 {
		t.Fatalf("expected 3 lock paths, got %d", len(paths))
	}
}

func TestWaitUntil(t *testing.T) {
	n := 0
	ok := waitUntil(500*time.Millisecond, 20*time.Millisecond, func() bool {
		n++
		return n >= 3
	})
	if !ok {
		t.Fatal("expected cond to become true")
	}
	if waitUntil(30*time.Millisecond, 10*time.Millisecond, func() bool { return false }) {
		t.Fatal("expected timeout failure")
	}
}

func TestProfileDirPath(t *testing.T) {
	p := profileDirPath()
	if p == "" || filepath.Base(p) != "browser-profile" {
		t.Errorf("unexpected profile dir %q", p)
	}
}
