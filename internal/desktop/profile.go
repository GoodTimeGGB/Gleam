// Package desktop helpers shared across platforms for Chromium --app profile handling.
package desktop

import (
	"os"
	"path/filepath"
	"time"
)

// profileDirPath returns the dedicated Chromium user-data-dir for Gleam --app windows.
// Isolating the profile keeps the shell out of the user's default Edge/Chrome session,
// but it also means a second spawn with the same dir hits SingletonLock and often paints
// a blank white window — callers must not spawn --app while the lock is held.
func profileDirPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".gleam", "browser-profile")
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

// shouldFallbackAfterAppLaunch is true when an --app spawn failed to produce a
// titled Gleam window within the readiness wait (blank/white shell, profile lock
// race, or broken Chrome --app on that machine). Callers should open the system
// default browser instead of leaving a useless empty window.
func shouldFallbackAfterAppLaunch(titledWindowCount int, readinessOK bool) bool {
	return !readinessOK || titledWindowCount <= 0
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
