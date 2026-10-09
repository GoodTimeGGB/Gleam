package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/buildinfo"
)

func TestChromeAppArgsIncludesProfileAndAppURL(t *testing.T) {
	args := chromeAppArgs("http://127.0.0.1:8787/", `C:\Users\x\.gleam\browser-profile\1.0.2`, "Gleam · 微光")
	for _, want := range []string{
		"--app=http://127.0.0.1:8787/",
		`--user-data-dir=C:\Users\x\.gleam\browser-profile\1.0.2`,
		"--app-name=Gleam · 微光",
		"--window-name=Gleam · 微光",
		"--no-first-run",
	} {
		if !containsLine(args, want) {
			t.Errorf("chromeAppArgs missing %q; got %v", want, args)
		}
	}
}

func TestChromeAppArgsWithGPUFallback(t *testing.T) {
	args := chromeAppArgsWithGPUFallback("http://x/", `C:\p`, "Gleam · 微光")
	for _, want := range []string{"--disable-gpu", "--disable-gpu-compositing", "--app=http://x/"} {
		if !containsLine(args, want) {
			t.Errorf("GPU fallback args missing %q; got %v", want, args)
		}
	}
	base := chromeAppArgs("http://x/", `C:\p`, "Gleam · 微光")
	if len(args) != len(base)+len(gpuSoftFallbackFlags()) {
		t.Errorf("len=%d want %d", len(args), len(base)+len(gpuSoftFallbackFlags()))
	}
}

func containsLine(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestAppShellReadyAndRetry(t *testing.T) {
	cases := []struct {
		n      int
		ok     bool
		ready  bool
		retry0 bool
		retry1 bool
	}{
		{0, false, false, true, false},
		{0, true, false, true, false},
		{1, false, false, true, false},
		{1, true, true, false, false},
		{2, true, true, false, false},
	}
	for _, c := range cases {
		if got := appShellReady(c.n, c.ok); got != c.ready {
			t.Errorf("appShellReady(%d,%v)=%v want %v", c.n, c.ok, got, c.ready)
		}
		if got := shouldRetryAppLaunch(0, c.n, c.ok); got != c.retry0 {
			t.Errorf("shouldRetryAppLaunch(0,%d,%v)=%v want %v", c.n, c.ok, got, c.retry0)
		}
		if got := shouldRetryAppLaunch(1, c.n, c.ok); got != c.retry1 {
			t.Errorf("shouldRetryAppLaunch(1,%d,%v)=%v want %v", c.n, c.ok, got, c.retry1)
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

func TestProfileDirPathVersioned(t *testing.T) {
	p := profileDirPath()
	if filepath.Base(p) != buildinfo.Version {
		t.Errorf("profile dir base = %q, want version %q (full %q)", filepath.Base(p), buildinfo.Version, p)
	}
	if !strings.Contains(p, filepath.Join(".gleam", "browser-profile")) {
		t.Errorf("expected ~/.gleam/browser-profile/<ver>, got %q", p)
	}
	fixed := profileDirPathFor("1.2.3")
	if filepath.Base(fixed) != "1.2.3" {
		t.Errorf("profileDirPathFor = %q", fixed)
	}
	empty := profileDirPathFor("")
	if filepath.Base(empty) != "dev" {
		t.Errorf("empty ver should be dev, got %q", empty)
	}
	alt := alternateProfileDir(fixed)
	if alt != fixed+"-alt" {
		t.Errorf("alternateProfileDir = %q", alt)
	}
}

func TestProfileDirPathSanitizes(t *testing.T) {
	p := profileDirPathFor("bad/ver:name")
	base := filepath.Base(p)
	if strings.ContainsAny(base, "/\\:*?\"<>|") {
		t.Errorf("unsanitized base %q", base)
	}
}
