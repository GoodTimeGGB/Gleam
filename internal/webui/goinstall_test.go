package webui

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gleam/internal/agent"
)

// buildGoZip 造一个「长得像」Go 归档的 zip：顶层 go/，里面一个可执行的 go(windows 下是 go.exe)，
// 外加一个普通文件、一个目录条目、一条越界路径。越界那条是判据的重点。
func buildGoZip(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("go/", "")
	add("go/bin/go", "#!/bin/sh\nexit 0\n")
	add("go/bin/go.exe", "MZ fake\n")
	add("go/pkg/tool/foo.txt", "data\n")
	add("../escape.txt", "should never land outside\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// rowByID 在台账里按 id 找一行（webui 侧的本地小工具，agent 包里另有一份）。
func rowByID(led agent.ConnectionLedger, id string) (agent.ConnectionRow, bool) {
	for _, r := range led.Rows {
		if r.ID == id {
			return r, true
		}
	}
	return agent.ConnectionRow{}, false
}

// TestGoArchiveNameAndMirrors 归档名与镜像序必须稳定：改名会同时打偏下载与校验和查找。
func TestGoArchiveNameAndMirrors(t *testing.T) {
	if got := goArchiveName("windows", "amd64", "1.23.4"); got != "go1.23.4.windows-amd64.zip" {
		t.Errorf("windows 归档名 = %q", got)
	}
	if got := goArchiveName("linux", "arm64", "1.23.4"); got != "go1.23.4.linux-arm64.tar.gz" {
		t.Errorf("linux 归档名 = %q", got)
	}
	urls := goMirrorURLs("windows", "amd64", "1.23.4")
	if len(urls) != 3 || !strings.Contains(urls[0], "mirrors.aliyun.com") {
		t.Errorf("镜像序应把国内源放前面：%v", urls)
	}
	for _, u := range urls {
		if !strings.HasSuffix(u, "go1.23.4.windows-amd64.zip") {
			t.Errorf("镜像地址没带上归档名：%q", u)
		}
	}
}

// TestCleanEntryStripsGoTopDirAndBlocksTraversal 顶层 go/ 要剥掉，越界路径要拦下。
func TestCleanEntryStripsGoTopDirAndBlocksTraversal(t *testing.T) {
	root := filepath.Join("C:", "tools")
	if got, ok := cleanEntry("go/bin/go.exe", root); !ok || got != filepath.Join(root, "go", "bin", "go.exe") {
		t.Errorf("剥掉顶层 go/ 后 = %q ok=%v", got, ok)
	}
	for _, bad := range []string{"../escape.txt", "go/../../escape.txt", "go/", "", ".", "go"} {
		if _, ok := cleanEntry(bad, root); ok {
			t.Errorf("%q 不该被接受", bad)
		}
	}
	// 反斜杠写法也要归一化后照样剥前缀（归档条目偶尔来自 Windows 打包）。
	if got, ok := cleanEntry("go\\pkg\\x", root); !ok || got != filepath.Join(root, "go", "pkg", "x") {
		t.Errorf("反斜杠条目 = %q ok=%v", got, ok)
	}
}

// TestExtractZipZipSlipIsBlocked 展开本身不许把文件写到目标目录之外。
func TestExtractZipZipSlipIsBlocked(t *testing.T) {
	dir := t.TempDir()
	zpath := filepath.Join(dir, "go.zip")
	buildGoZip(t, zpath)

	root := filepath.Join(dir, "tools")
	if err := extractGoArchive(zpath, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go", "pkg", "tool", "foo.txt")); err != nil {
		t.Errorf("归档内容没落到 <root>/go 下：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Error("越界条目被写到了目标目录之外")
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); err == nil {
		t.Error("越界条目被写进了展开目录")
	}
}

// TestInstallGoToolchainServesFromMirror 端到端：本地镜像 + 本地校验和清单 → 装好并认出。
func TestInstallGoToolchainServesFromMirror(t *testing.T) {
	f := newFixture(t, nil)

	dir := t.TempDir()
	zpath := filepath.Join(dir, "archive.zip")
	buildGoZip(t, zpath)
	body, err := os.ReadFile(zpath)
	if err != nil {
		t.Fatal(err)
	}
	name := goArchiveName(runtime.GOOS, goArch(), goToolchainVersion)

	var base string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+name):
			w.Write(body)
		case r.URL.Path == "/dl":
			rels := []map[string]any{{
				"version": "go" + goToolchainVersion,
				"files": []map[string]string{
					{"filename": name, "sha256": sha256Hex(body)},
				},
			}}
			_ = json.NewEncoder(w).Encode(rels)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	base = ts.URL

	old := goChecksumEndpoints
	goChecksumEndpoints = []string{base + "/dl"}
	defer func() { goChecksumEndpoints = old }()

	dest := filepath.Join(t.TempDir(), "tools", "go")
	res := f.srv.installGoToolchain(context.Background(), name, dest, []string{base + "/" + name})

	if ok, _ := res["found"].(bool); !ok {
		t.Fatalf("安装未成功：%v", res)
	}
	if ok, _ := res["verified"].(bool); !ok {
		t.Error("本地清单拿得到时应标记 verified")
	}
	if res["source"] != "gleam-managed" {
		t.Errorf("来源应标成 gleam-managed：%v", res["source"])
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "go.exe")); err != nil {
		if _, err2 := os.Stat(filepath.Join(dest, "bin", "go")); err2 != nil {
			t.Errorf("go 可执行文件没展开到 bin/：%v / %v", err, err2)
		}
	}

	// 出网留痕必须真的落到门控里——台账那一行才有读数。
	rep := f.agent.Gate.EgressStats()
	found := false
	for _, st := range rep.Stats {
		if st.Kind == "go.toolchain" && st.Count > 0 && st.Bytes > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("下载没有记 go.toolchain 出网留痕：%+v", rep.Stats)
	}
}

// TestInstallGoToolchainRejectsBadChecksum 校验和对不上必须中止，不能装上一个可疑文件。
func TestInstallGoToolchainRejectsBadChecksum(t *testing.T) {
	f := newFixture(t, nil)

	dir := t.TempDir()
	zpath := filepath.Join(dir, "archive.zip")
	buildGoZip(t, zpath)
	body, err := os.ReadFile(zpath)
	if err != nil {
		t.Fatal(err)
	}
	name := goArchiveName(runtime.GOOS, goArch(), goToolchainVersion)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+name):
			w.Write(body)
		case r.URL.Path == "/dl":
			rels := []map[string]any{{
				"version": "go" + goToolchainVersion,
				"files": []map[string]string{
					{"filename": name, "sha256": strings.Repeat("0", 64)},
				},
			}}
			_ = json.NewEncoder(w).Encode(rels)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	old := goChecksumEndpoints
	goChecksumEndpoints = []string{ts.URL + "/dl"}
	defer func() { goChecksumEndpoints = old }()

	dest := filepath.Join(t.TempDir(), "tools", "go")
	res := f.srv.installGoToolchain(context.Background(), name, dest, []string{ts.URL + "/" + name})

	if ok, _ := res["found"].(bool); ok {
		t.Fatalf("校验和不对还是装上了：%v", res)
	}
	msg, _ := res["message"].(string)
	if !strings.Contains(msg, "sha256") {
		t.Errorf("失败原因该点明校验和对不上：%q", msg)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("校验失败时不该留下目标目录")
	}
}

// TestGoToolchainRowPresent 台账必须为这条新出网路径画出行，且每格都有话。
func TestGoToolchainRowPresent(t *testing.T) {
	f := newFixture(t, nil)
	r, ok := rowByID(f.agent.ConnectionView("127.0.0.1:8787"), "go.toolchain")
	if !ok {
		t.Fatal("缺少 go.toolchain 台账行")
	}
	if r.Kind != "out" || strings.TrimSpace(r.Stats) == "" {
		t.Errorf("这一行应是出网且有读数文案：%+v", r)
	}
	if !strings.Contains(r.Status, "待命") {
		t.Errorf("没装之前该说待命：%q", r.Status)
	}
}
