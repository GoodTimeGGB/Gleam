// Package desktop helpers shared across platforms for Chromium --app profile handling.
package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gleam/internal/buildinfo"
)

// profileDirPath returns the dedicated Chromium user-data-dir for Gleam --app windows.
// Isolating the profile keeps the shell out of the user's default Edge/Chrome session.
// The path is versioned (~/.gleam/browser-profile/<ver>) so an upgrade does not reuse
// a lock/profile state that belonged to an older Desktop build.
func profileDirPath() string {
	return profileDirPathFor(buildinfo.Version)
}

// profileDirPathFor builds ~/.gleam/browser-profile/<ver>. Empty ver becomes "dev".
func profileDirPathFor(ver string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	ver = strings.TrimSpace(ver)
	if ver == "" {
		ver = "dev"
	}
	ver = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		default:
			return r
		}
	}, ver)
	return filepath.Join(home, ".gleam", "browser-profile", ver)
}

// alternateProfileDir returns the one-shot retry profile sibling (primary + "-alt").
// Used when the first --app spawn fails to paint; keeps the primary profile intact.
func alternateProfileDir(primary string) string {
	if primary == "" {
		return "browser-profile-alt"
	}
	return primary + "-alt"
}

// chromiumProfileLockNames are files Chromium writes under user-data-dir while a
// browser process owns the profile. Presence alone is not proof of a live lock
// (crash can leave stale files); combine with titled-window / process checks.
func chromiumProfileLockNames() []string {
	return []string{"SingletonLock", "SingletonCookie", "lockfile"}
}

// profileLockPaths returns absolute paths of Chromium lock markers under dir.
func profileLockPaths(dir string) []string {
	names := chromiumProfileLockNames()
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(dir, n))
	}
	return out
}

// anyProfileLockPresent reports whether any Chromium lock marker exists under dir.
func anyProfileLockPresent(dir string) bool {
	for _, p := range profileLockPaths(dir) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// removeStaleProfileLocks deletes Chromium lock markers when the caller has already
// confirmed there is no live Gleam --app window. Safe to call on missing files.
func removeStaleProfileLocks(dir string) {
	for _, p := range profileLockPaths(dir) {
		_ = os.Remove(p)
	}
}

// waitUntil reports whether cond became true before timeout, polling every interval.
func waitUntil(timeout, interval time.Duration, cond func() bool) bool {
	if cond == nil {
		return false
	}
	if cond() {
		return true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		if cond() {
			return true
		}
	}
	return cond()
}

// chromeAppArgs builds the Edge/Chrome --app argument list for a Gleam shell window.
func chromeAppArgs(target, profile, appName string) []string {
	return []string{
		"--app=" + target,
		"--window-size=1280,840",
		"--app-name=" + appName,
		"--window-name=" + appName,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection,Translate",
		"--disable-session-crashed-bubble",
		"--disable-infobars",
	}
}

// gpuSoftFallbackFlags are optional Chromium flags for a single retry when the first
// --app spawn fails to produce a titled window (GPU/compositor glitches on some GPUs).
func gpuSoftFallbackFlags() []string {
	return []string{
		"--disable-gpu",
		"--disable-gpu-compositing",
	}
}

// chromeAppArgsWithGPUFallback is chromeAppArgs plus GPU soft-fallback flags.
func chromeAppArgsWithGPUFallback(target, profile, appName string) []string {
	return append(chromeAppArgs(target, profile, appName), gpuSoftFallbackFlags()...)
}

// appShellReady is true when --app produced a titled Gleam window within the readiness wait.
func appShellReady(titledWindowCount int, readinessOK bool) bool {
	return readinessOK && titledWindowCount > 0
}

// shouldRetryAppLaunch reports whether the first --app attempt failed and a single
// alternate-flags/profile retry should run. attempt is 0-based (only attempt 0 retries).
func shouldRetryAppLaunch(attempt int, titledWindowCount int, readinessOK bool) bool {
	return attempt == 0 && !appShellReady(titledWindowCount, readinessOK)
}

// preferEdgeOverChrome keeps Edge before Chrome in the candidate list.
// Chrome --app with a dedicated user-data-dir is a common source of blank shells
// on some Windows installs; Edge is preferred when both are present.
func preferEdgeOverChrome(paths []string) []string {
	if len(paths) <= 1 {
		return paths
	}
	var edge, rest []string
	for _, p := range paths {
		base := filepath.Base(p)
		if base == "msedge.exe" {
			edge = append(edge, p)
			continue
		}
		rest = append(rest, p)
	}
	return append(edge, rest...)
}
