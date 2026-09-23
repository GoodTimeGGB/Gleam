package main

import (
	"strings"
	"testing"
)

// 跨平台行为矩阵：三个平台的浏览器候选路径与回退命令序列。
// macOS 分支无法在无 Mac 真机时执行，但此处把 darwin 的期望行为固化为确定性断言，
// 配合 GOOS=darwin 的交叉编译与 vet，构成 mac 端可做的最强静态+逻辑验证。
func TestPlatformBrowserMatrix(t *testing.T) {
	// ---------- darwin ----------
	paths := browserPathsFor("darwin")
	joined := strings.Join(paths, "\n")
	for _, want := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("darwin 候选缺少 %s", want)
		}
	}
	fb := browserFallbackCmds("darwin", "http://127.0.0.1:8787/")
	if len(fb) != 4 {
		t.Fatalf("darwin 回退命令数 = %d，应为 4（Chrome/Edge/Chromium 应用模式 + 默认浏览器）", len(fb))
	}
	if fb[0][0] != "open" || fb[0][1] != "-na" || fb[0][2] != "Google Chrome" {
		t.Errorf("darwin 首选回退 = %v", fb[0])
	}
	foundAppArg := false
	for _, c := range fb {
		if len(c) >= 4 && c[0] == "open" && c[1] == "-na" {
			for _, a := range c {
				if a == "--app=http://127.0.0.1:8787/" {
					foundAppArg = true
				}
			}
		}
	}
	if !foundAppArg {
		t.Error("darwin 应用模式回退应携带 --app= 参数")
	}
	if last := fb[len(fb)-1]; len(last) != 2 || last[0] != "open" || last[1] != "http://127.0.0.1:8787/" {
		t.Errorf("darwin 最终回退应为 open url: %v", last)
	}

	// ---------- windows ----------
	// 候选顺序依赖 ProgramFiles / ProgramFiles(x86) 环境变量。精简环境（CI 容器、
	// 某些终端沙箱）里这两个变量可能为空，届时 Edge 候选会整个消失、Chrome 变成首项，
	// 断言就假红。所以显式注入，让这条断言只测「顺序契约」本身。
	t.Setenv("ProgramFiles", `C:\PF`)
	t.Setenv("ProgramFiles(x86)", `C:\PF86`)
	t.Setenv("LocalAppData", `C:\LAD`)

	wp := browserPathsFor("windows")
	if len(wp) == 0 || !strings.HasSuffix(wp[0], "msedge.exe") {
		t.Errorf("windows 候选首项应为 Edge: %v", wp)
	}
	if !strings.Contains(strings.Join(wp, "\n"), `C:\PF86\Microsoft\Edge\Application\msedge.exe`) {
		t.Errorf("未按 ProgramFiles(x86) 生成 Edge 候选: %v", wp)
	}
	// 契约：Edge 全部排在 Chrome 之前——优先用系统自带的 Edge，
	// 用户没装 Chrome 也要能开出窗口；Chrome 只作兜底。
	firstChrome := -1
	for i, p := range wp {
		if strings.HasSuffix(p, "chrome.exe") {
			firstChrome = i
			break
		}
	}
	if firstChrome < 0 {
		t.Errorf("windows 候选应包含 Chrome 兜底: %v", wp)
	} else {
		for i := 0; i < firstChrome; i++ {
			if !strings.HasSuffix(wp[i], "msedge.exe") {
				t.Errorf("Chrome 之前应全是 Edge 候选，第 %d 项是 %s", i, wp[i])
			}
		}
	}
	wfb := browserFallbackCmds("windows", "http://x/")
	if len(wfb) != 1 || wfb[0][0] != "rundll32" {
		t.Errorf("windows 回退 = %v", wfb)
	}

	// ---------- linux ----------
	if lp := browserPathsFor("linux"); len(lp) == 0 || lp[0] != "/usr/bin/google-chrome" {
		t.Errorf("linux 候选 = %v", lp)
	}
	if lfb := browserFallbackCmds("linux", "http://x/"); len(lfb) != 1 || lfb[0][0] != "xdg-open" {
		t.Errorf("linux 回退 = %v", lfb)
	}
}
