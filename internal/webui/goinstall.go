package webui

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gleam/internal/buildinfo"
)

// Go 工具链真安装。
//
// 三条自我约束，和为「检查更新」定的那三条同源：
//  1. **只在用户点了「安装 Go」之后才下载**。检测阶段只读本机，一个包都不发。
//  2. **装进 Gleam 自己的 tools 目录**（<数据目录>/tools/go），不碰机器的全局 PATH、
//     不写 C:\Go。理由不是偷懒：写全局 PATH 是替用户改系统状态，而 Go 在这里的用途
//     只有「给 Gleam 编译技能插件」——够用即可，越界就得先问。
//  3. **校验和能拿到就强校验**：优先向官方要 sha256 清单，命中就比，比不过当场中止。
//     清单拉不到（被墙 / 断网）不算失败，但响应里会说 `verified: false`——不把
//     "没校验"讲成"校验过"。
//
// 下载源按顺序试：阿里云镜像 → 官方国内镜像 → 官方站。国内优先是因为这条路径
// 最常被 wall 卡住，而 go.zip 有 80MB 量级，卡住就是几分钟白等。

// goToolchainVersion 是 Gleam 拉取的 Go 版本。与 scripts/install.ps1 的 -GoVersion 对齐。
const goToolchainVersion = "1.23.4"

// goToolsRoot 工具链落点。传空 dataDir 时退回用户主目录，保证调用方忘传也能算出个位置。
func goToolsRoot(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".gleam")
		}
		return ".gleam"
	}
	return dataDir
}

// goInstallDir 本次安装会写到哪里：<根>/tools/go。
func goInstallDir(dataDir string) string {
	return filepath.Join(goToolsRoot(dataDir), "tools", "go")
}

// goArchiveName 拼归档文件名。名字与官方下载页一一对应，sha256 清单也按它查。
func goArchiveName(goos, arch, ver string) string {
	if goos == "windows" {
		return fmt.Sprintf("go%s.%s-%s.zip", ver, goos, arch)
	}
	return fmt.Sprintf("go%s.%s-%s.tar.gz", ver, goos, arch)
}

// goMirrorURLs 归档的候选下载地址，按优先级排列。
func goMirrorURLs(goos, arch, ver string) []string {
	name := goArchiveName(goos, arch, ver)
	return []string{
		"https://mirrors.aliyun.com/golang/" + name,
		"https://golang.google.cn/dl/" + name,
		"https://go.dev/dl/" + name,
	}
}

// goChecksumEndpoints 官方 sha256 清单的来源，按优先级排列。
// 声明成变量而非函数，是为了测试能把它换成本地服务——判据不该依赖真连上 golang.google.cn。
var goChecksumEndpoints = []string{
	"https://golang.google.cn/dl/?mode=json&include=all",
	"https://go.dev/dl/?mode=json&include=all",
}

// goArch 归一化当前架构名。官方归档只认 amd64 / arm64。
func goArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

// handleGoInstall 下载并展开 Go 工具链到 Gleam 的 tools 目录。
func (s *Server) handleGoInstall(w http.ResponseWriter, r *http.Request) {
	if st := s.managedGoStatus(); st.Found {
		writeJSON(w, 200, st)
		return
	}

	goos, arch := runtime.GOOS, goArch()
	name := goArchiveName(goos, arch, goToolchainVersion)
	dest := goInstallDir(s.Agent.DataDir())

	res := s.installGoToolchain(r.Context(), name, dest, goMirrorURLs(goos, arch, goToolchainVersion))
	if found, _ := res["found"].(bool); found {
		writeJSON(w, 200, res)
		return
	}
	writeJSON(w, 200, res)
}

// installGoToolchain 是 handleGoInstall 的真身，拆出来是为了能在测试里只喂一个
// 本地 http 服务当镜像源，不必真连阿里云。
func (s *Server) installGoToolchain(ctx context.Context, name, dest string, urls []string) map[string]any {
	out := map[string]any{
		"found":   false,
		"name":    name,
		"target":  dest,
		"sources": urls,
		"version": "go" + goToolchainVersion,
	}

	// 校验和清单先拿：拿到了就一路强校验，拿不到继续但如实标记。
	want, verified := fetchGoSHA256(ctx, name)
	out["verified"] = verified

	tmpDir := filepath.Join(goToolsRoot(s.Agent.DataDir()), "tools", ".download")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		out["message"] = "创建下载目录失败：" + err.Error()
		return out
	}
	archive := filepath.Join(tmpDir, name)
	defer os.Remove(archive)

	var lastErr string
	for _, u := range urls {
		n, err := s.fetchTo(ctx, u, archive)
		if err != nil {
			lastErr = fmt.Sprintf("%s：%v", hostOf(u), err)
			continue
		}
		if verified {
			got, err := sha256File(archive)
			if err != nil {
				lastErr = "算校验和失败：" + err.Error()
				continue
			}
			if !strings.EqualFold(got, want) {
				lastErr = fmt.Sprintf("%s 下到的文件 sha256 与官方清单不符，已丢弃", hostOf(u))
				_ = os.Remove(archive)
				continue
			}
		}
		_ = n
		out["bytes"] = n
		if err := os.RemoveAll(dest); err != nil {
			out["message"] = "清理旧目录失败：" + err.Error()
			return out
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			out["message"] = "创建目标目录失败：" + err.Error()
			return out
		}
		if err := extractGoArchive(archive, filepath.Dir(dest)); err != nil {
			out["message"] = "解压失败：" + err.Error()
			return out
		}
		if st := DetectGo(goBinDir(dest)); st.Found {
			st.Source = "gleam-managed"
			out["found"] = true
			out["path"] = st.Path
			out["version"] = st.Version
			out["bin_dir"] = st.BinDir
			out["root"] = st.Root
			out["source"] = "gleam-managed"
			return out
		}
		out["message"] = "展开完成但没找到 go 可执行文件，目录可能不完整"
		return out
	}

	out["message"] = "下载失败，已试过所有镜像源：" + lastErr
	out["download"] = urls[0]
	return out
}

// goBinDir 归档里 go 可执行文件所在的 bin 目录。
func goBinDir(root string) string { return filepath.Join(root, "bin") }

// managedGoStatus 先看 Gleam 自己装的那一份（<数据目录>/tools/go），再看系统。
// 顺序不能反：用户点了「安装 Go」之后，界面上那一行必须立刻变成"已检测到"，
// 而系统 PATH 里没有这份新装的 Go——只有这里先认它，安装才算闭环。
func (s *Server) managedGoStatus(customPath ...string) GoStatus {
	if len(customPath) == 0 && s.Agent != nil {
		if st := DetectGo(goBinDir(goInstallDir(s.Agent.DataDir()))); st.Found {
			st.Source = "gleam-managed"
			return st
		}
	}
	return DetectGo(customPath...)
}

// fetchTo 把一个 URL 下到本地文件，并把「出网」记进门控留痕。
// 返回下到的字节数。响应体上限 512MB，够放下任何一版 Go 归档。
func (s *Server) fetchTo(ctx context.Context, raw, dst string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "gleam/"+buildinfo.Version)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, 512<<20))
	closeErr := f.Close()
	if copyErr != nil {
		return n, copyErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	// 留痕只记主机与字节，不记 URL 全貌——和 web.fetch 同一口径。
	if s.Agent != nil && s.Agent.Gate != nil {
		s.Agent.Gate.RecordEgress("go.toolchain", hostOf(raw), int(n))
	}
	return n, nil
}

// hostOf 从 URL 里取主机名，取不到就回原串（留痕宁可难看也不要空着）。
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// fetchGoSHA256 向官方清单要这个文件的 sha256。拿不到返回 ok=false，不当作错误。
func fetchGoSHA256(ctx context.Context, filename string) (string, bool) {
	client := &http.Client{Timeout: 15 * time.Second}
	for _, src := range goChecksumEndpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "gleam/"+buildinfo.Version)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var rels []struct {
			Files []struct {
				Filename string `json:"filename"`
				SHA256   string `json:"sha256"`
			} `json:"files"`
		}
		// 清单可以很大（include=all），但一到两 MB 足够，别把整个响应塞进内存。
		decErr := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels)
		resp.Body.Close()
		if decErr != nil {
			continue
		}
		for _, rel := range rels {
			for _, f := range rel.Files {
				if f.Filename == filename && f.SHA256 != "" {
					return f.SHA256, true
				}
			}
		}
	}
	return "", false
}

// sha256File 算一个文件的 sha256，十六进制小写。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractGoArchive 按归档类型展开。dstRoot 是「go/」上一层（即 tools 目录），
// 归档里的顶层 go/ 会被剥掉，最终落到 <dstRoot>/go。
func extractGoArchive(archive, dstRoot string) error {
	if strings.HasSuffix(archive, ".zip") {
		return extractZip(archive, dstRoot)
	}
	return extractTarGz(archive, dstRoot)
}

// cleanEntry 把归档条目路径归一化，并挡掉目录穿越（zip slip）。
//
// 官方 Go 归档的顶层是 `go/`，所以要剥掉一段——剥完落到 dstRoot 下。
// `..` 一律拒绝：宁可让一次安装在恶意归档上失败，也不要往目标目录之外写文件。
func cleanEntry(name, dstRoot string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimPrefix(name, "go/")
	if name == "" || name == "." || name == "go" {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." || filepath.IsAbs(clean) {
		return "", false
	}
	return filepath.Join(dstRoot, "go", clean), true
}

// insideRoot 复核 target 确实落在 root 之内，就地用于写入之前。
//
// cleanEntry 已经在归一化时挡过目录穿越，这里是**就地可见的第二道**：解压是真正往盘上
// 写文件的那一步，多一道便宜的复核不亏，而且"写到哪儿"在调用点就能读出来。
//
// 判定用 `前缀 + 分隔符` 而不是 filepath.Rel：`.../go` 与 `.../gox` 这种同前缀目录，
// 只比前缀会误判，补上分隔符才严格。两侧都先 Clean，目标必须仍是绝对路径。
func insideRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if !filepath.IsAbs(target) {
		return false
	}
	return target != root && strings.HasPrefix(target, root+string(filepath.Separator))
}

// extractZip 展开 Windows 归档。逐条写，符号链接一律跳过——Go 的 zip 里不该有，
// 真有就说明来路不对。
func extractZip(archive, dstRoot string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		target, ok := cleanEntry(f.Name, dstRoot)
		if !ok || !insideRoot(dstRoot, target) {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := writeFileFrom(target, rc, mode); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return nil
}

// extractTarGz 展开 macOS / Linux 归档。
func extractTarGz(archive, dstRoot string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, ok := cleanEntry(hdr.Name, dstRoot)
		if !ok || !insideRoot(dstRoot, target) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			if err := writeFileFrom(target, io.LimitReader(tr, hdr.Size), mode); err != nil {
				return err
			}
		default:
			// 符号链接 / 硬链接 / 设备节点都跳过：Go 官方归档不含这些，出现即异常。
		}
	}
	return nil
}

// writeFileFrom 流式写出一个文件，写完才关，任何一步失败都把半截文件删掉。
func writeFileFrom(target string, src io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(target)
		return err
	}
	return out.Close()
}
