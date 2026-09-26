// Package feedback 存用户提交的反馈与建议，并（在配了远端时）把它再投出去一份。
//
// 为什么本地是必选、远端是可插拔：Gleam 是本地优先的桌面端，网络、账号、远端表
// 任何一样不在，反馈都不该因此丢掉。所以顺序固定为**先落盘、再谈投递**——
// 与任务归档（§4.6.27.3）同一条理由：数据在，界面说没有，比没数据更坏。
package feedback

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gleam/internal/atomicfile"
	"gleam/pkg/types"
)

// maxAttachmentBytes 单张截图上限。截图原图常在 1–3MB，5MB 够又挡住"顺手塞个视频"。
const maxAttachmentBytes = 5 << 20

// idPattern 反馈 id 的形状。id 由 NewID 生成，读侧只认这个形状：
// 这样"文件名来自调用方"这件事根本不成立，路径穿越也就没有可传的地方。
var idPattern = regexp.MustCompile(`^fb-[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// attachmentPattern 截图名的主体部分（不含扩展名），扩展名单独按白名单判。
var attachmentPattern = regexp.MustCompile(`^fb-[0-9]{8}-[0-9]{6}-[0-9a-f]{4}-[0-9]+$`)

// Store 一个数据目录下的反馈仓库：<dataDir>/feedback/<id>.json 与 <id>-<n>.<ext>。
type Store struct {
	dataDir string
}

func NewStore(dataDir string) *Store { return &Store{dataDir: dataDir} }

// NewID 造一个可读、可排序、可去重的 id：时间戳在前（列表按它排就是按提交序），
// 随机后缀在后（同一秒内两次提交不会撞，也不会互相覆盖）。
func NewID() string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 随机源不可用时退回时间纳秒位：宁可 id 熵低，也不要"提交反馈"这件事失败。
		now := time.Now().UnixNano()
		b[0] = byte(now)
		b[1] = byte(now >> 8)
	}
	now := time.Now()
	return fmt.Sprintf("fb-%s-%s", now.Format("20060102-150405"), hex.EncodeToString(b[:]))
}

func (s *Store) dir() string { return filepath.Join(s.dataDir, "feedback") }

// path 反馈 JSON 的路径；id 不合形状就返回空（视作"不存在"，不写也不读）。
func (s *Store) path(id string) string {
	if !idPattern.MatchString(id) {
		return ""
	}
	return filepath.Join(s.dir(), id+".json")
}

// Save 落一条反馈。**写成功之前，调用方不该认为它提交过了。**
func (s *Store) Save(f *types.Feedback) error {
	if f == nil {
		return fmt.Errorf("没有可提交的反馈")
	}
	if s.dataDir == "" {
		return fmt.Errorf("未配置数据目录，反馈未提交")
	}
	path := s.path(f.ID)
	if path == "" {
		return fmt.Errorf("反馈 ID %q 不合形状，未提交", f.ID)
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	b, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return fmt.Errorf("序列化反馈失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, b, 0o644)
}

// Read 读回一条反馈。"没有这条"返回 (nil, nil)——与任务归档同口径：
// 没提交过是常态，做成错误会让每个调用点绕一次预期中的失败。
// 读到了却解析不动是真错误（文件坏了），必须报出来。
func (s *Store) Read(id string) (*types.Feedback, error) {
	path := s.path(id)
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f types.Feedback
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("解析反馈失败（%s）: %w", path, err)
	}
	if f.ID != id {
		return nil, fmt.Errorf("反馈内容与文件名不符（%s 里写的是 %q）", path, f.ID)
	}
	return &f, nil
}

// Delete 删掉一条反馈连同它的截图。
//
// 带截图的东西必须能删：屏幕上是别人的聊天、别的公司的页面时，
// "留在你机器上了"这句话本身就是一个要交代的答复。
func (s *Store) Delete(id string) error {
	path := s.path(id)
	if path == "" {
		return nil // 不合形状：这里头本来就没有东西可删
	}
	names, _ := filepath.Glob(filepath.Join(s.dir(), id+"-*"))
	for _, n := range names {
		if err := os.Remove(n); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List 按提交时间倒序列出反馈，最多 limit 条；第二个返回值是没读动的文件数。
// 单条坏文件只让它自己消失并计数，不把整个列表打翻（同任务归档的判据）。
func (s *Store) List(limit int) ([]*types.Feedback, int, error) {
	if limit <= 0 {
		limit = 50
	}
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	type entry struct {
		id  string
		mod time.Time
	}
	var files []entry
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{strings.TrimSuffix(name, ".json"), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	out := make([]*types.Feedback, 0, limit)
	skipped := 0
	for _, f := range files {
		if len(out) >= limit {
			break
		}
		item, err := s.Read(f.id)
		if err != nil || item == nil {
			skipped++
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, skipped, nil
}

// SaveAttachment 存一张截图并返回落盘文件名。
//
// 类型只认文件头，不认扩展名，也不认调用方声称的 MIME：一个改名成 .png 的脚本
// 不该因为前端写了 image/png 就被当图片存下、再被当图片发出去。
// 文件名由后端拼（id + 序号 + sniff 出的扩展名），所以传什么都进不了路径。
func (s *Store) SaveAttachment(id string, data []byte) (string, string, error) {
	if !idPattern.MatchString(id) {
		return "", "", fmt.Errorf("反馈 ID %q 不合形状", id)
	}
	if len(data) == 0 {
		return "", "", fmt.Errorf("截图是空的")
	}
	if len(data) > maxAttachmentBytes {
		return "", "", fmt.Errorf("截图 %d MB 超过 %d MB 上限",
			len(data)>>20, maxAttachmentBytes>>20)
	}
	ext, ok := imageExt(data)
	if !ok {
		return "", "", fmt.Errorf("不是支持的图片类型（只收 PNG / JPEG / GIF / WebP）")
	}
	// 序号取已有同类文件的下一个：同一张图重复粘贴不该盖掉前一张。
	n := 1
	for {
		name := fmt.Sprintf("%s-%d%s", id, n, ext)
		if _, err := os.Stat(filepath.Join(s.dir(), name)); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", "", err
		}
		n++
	}
	name := fmt.Sprintf("%s-%d%s", id, n, ext)
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return "", "", err
	}
	// 0600 而不是反馈 JSON 的 0644：截图截到什么不由我们决定，可能是别人的窗口。
	if err := atomicfile.Write(filepath.Join(s.dir(), name), data, 0o600); err != nil {
		return "", "", err
	}
	return name, sniffMIME(data), nil
}

// ReadAttachment 读出截图。name 必须是自己写出去的那个形状，否则视为不存在。
func (s *Store) ReadAttachment(name string) ([]byte, string, error) {
	if !safeAttachmentName(name) {
		return nil, "", nil
	}
	b, err := os.ReadFile(filepath.Join(s.dir(), name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	return b, sniffMIME(b), nil
}

// safeAttachmentName：<id>-<n>.<ext>，扩展名只允许那四种。`..` 与分隔符进不了这个形状。
func safeAttachmentName(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return attachmentPattern.MatchString(base) && imageExtAllowed(filepath.Ext(name))
}

func imageExtAllowed(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func imageExt(data []byte) (string, bool) {
	switch sniffMIME(data) {
	case "image/png":
		return ".png", true
	case "image/jpeg":
		return ".jpg", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	}
	return "", false
}

func sniffMIME(data []byte) string {
	if len(data) > 512 {
		data = data[:512]
	}
	return http.DetectContentType(data)
}
