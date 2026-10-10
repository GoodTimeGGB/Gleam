package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gleam/internal/atomicfile"
	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// 写前快照：动文件之前，先把这个路径**当时**的样子存一份。
//
// **为什么需要它**：`file.write` 是 `O_TRUNC` 直写（`tools/file/file.go:200`），旧内容在
// 写入那一刻就没有第二个副本了。于是"这次任务改了什么"只能回答"我写了什么"，而
// "改了什么"是个**对比**——没有写前那一份，对比无从谈起，还原同样无从谈起。
// 用户敢把 Agent 放进自己的工作目录，前提恰恰是"弄坏了能退回去"。
//
// **为什么挂在执行器、而不是文件工具里**：快照必须知道"我是哪次任务的哪一步"，
// 而工具实现的签名里没有 taskID，也不该有（加进去等于让每个工具都学会任务概念）。
// 执行器两头都知道，所以挂在这里。
//
// **为什么不引 git**：零第三方依赖是产品前提，而且工作区**可能根本不是仓库**
// （用户也可能故意不要版本控制）。用 git 会把"能还原"变成"恰好装了 git 且恰好
// 是仓库"——那是一条会静默失效的承诺，正是本仓库反复栽的那类。
//
// **单位是路径，不是步骤**：一条 `file.move` 同时端掉两个路径，一个任务里同一路径
// 可能被写三次。快照按"路径 + 时间"记，还原按"路径"退到**本任务开始前**——
// 这是用户点"还原"时心里想的那件事，而不是"撤销第 7 步"（逐步撤销要按逆序回放
// 副作用，是另一个量级的东西，见 docs/known-limits.md「完整断点续跑」）。
//
// 文件名为什么不叫 snapshot.go：那里已经住着**配置快照**的测试（`snapshot_test.go`
// 测的是 `GoalResult.ConfigSnapshot`）。两个"快照"是两件事，同名同住只会让下一个人读错。

// 落盘目录名与上限。
const (
	snapshotDirName  = "snapshots"
	manifestFileName = "manifest.json"
	snapshotMaxBytes = 1 << 20  // 单文件超过就不存内容，只记"当时多大"
	snapshotMaxTotal = 32 << 20 // 一个任务的快照内容总量上限
	snapshotMaxTasks = 200      // 保留的任务目录数，与 spillMaxTasks 同一个意图
	snapshotSniffLen = 8 * 1024 // 判文本/二进制时看前多少字节
)

// preImage 一条写前快照：某个路径在被本任务动到**之前**是什么样。
//
// 刻意不外露（`types.FileChange` 才是出口）：这张清单的形态跟着快照目录走，
// 泄漏到跨层类型里，下一个改它的人就得同时考虑界面兼容。
type preImage struct {
	Seq    int    `json:"seq"`
	StepID string `json:"step_id"`
	Tool   string `json:"tool"`
	Path   string `json:"path"` // 绝对路径（由工具自己的边界校验给出）
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes,omitempty"` // Exists 时的当时字节数
	IsDir  bool   `json:"is_dir,omitempty"`
	// Mode 当时的文件权限位。还原必须带上它：一个可执行脚本被退成 0644 之后
	// 内容是对的、跑不起来，而"还原把它弄坏了"和"还原没还原成"是同一类投诉。
	Mode    uint32 `json:"mode,omitempty"`
	File    string `json:"file,omitempty"`   // 内容文件名；空=没留住内容
	Reason  string `json:"reason,omitempty"` // 没留住内容的原因（原样显示给用户）
	ModTime string `json:"mod_time,omitempty"`
	At      string `json:"at"`
}

// reversible 这条快照够不够把路径退回去。
//
// 两种情形够：当时不存在（还原=删掉后来造出来的东西）、当时是文件且留住了内容。
// 当时是文件却没留住内容就是真不行——覆盖写下去那份就没了，只能如实报不可还原。
func (p preImage) reversible() bool {
	return !p.Exists || p.File != ""
}

// SnapshotRoot 快照根目录；未配置数据目录时返回空串。
func SnapshotRoot(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	return filepath.Join(dataDir, snapshotDirName)
}

// PruneSnapshots 按修改时间保留最新的 keep 个任务快照目录，其余删除。
// 与 PruneSpill 同一套裁剪（见 pruneTaskDirs）。
func PruneSnapshots(dataDir string, keep int) {
	pruneTaskDirs(SnapshotRoot(dataDir), keep)
}

// snapshotter **一个任务**的快照记录器（一次目标可以跑好几轮 Execute）。零值表示未启用。
type snapshotter struct {
	dir          string
	manifestPath string

	mu   sync.Mutex
	recs []preImage
	used int64 // 已落盘的快照内容总量
}

// newSnapshotter 建**任务级**记录器。taskID 过 safeSeg，再过一次 ResolveInRoots——
// 理由与 SpillOutput 相同：两套校验一定会漂移，宁可复用已在别处验证过的那一条。
//
// 建的时候回读磁盘上已有的清单（见 load）：一次目标可以跑好几轮 Execute。
func newSnapshotter(dataDir, taskID string) *snapshotter {
	root := SnapshotRoot(dataDir)
	if root == "" {
		return &snapshotter{}
	}
	dir := filepath.Join(root, safeSeg(taskID))
	manifest := filepath.Join(dir, manifestFileName)
	if _, err := toolutil.ResolveInRoots(manifest, []string{root}); err != nil {
		return &snapshotter{}
	}
	s := &snapshotter{dir: dir, manifestPath: manifest}
	s.load()
	return s
}

// load 把本任务此前留下的快照接回内存。
//
// 归属在**任务**而不在一次 Execute：规划可以失败重来（重规划、回落直聊），
// 每轮各起一份空清单的话，上一轮那些真实写入就从改动清单里消失了——
// 盘上多了文件、界面说"没动过"，而这正是这一层唯一不许出现的方向（§4.6.32 ③ 不许漏报）。
// 更要紧的是 `firstPreImageOf` 取的是**最早**那一份：不接上上一轮，第二轮的"写前"
// 就成了第一轮写出来的内容，还原只能退到任务中间，§4.6.32 ④ 那条唯一时间线守不住。
// 读不到就算了（首次运行、或清单坏了）：快照是保险，不是前提。
func (s *snapshotter) load() {
	data, err := os.ReadFile(s.manifestPath)
	if err != nil {
		return
	}
	var recs []preImage
	if json.Unmarshal(data, &recs) != nil {
		return
	}
	s.recs = recs
	for _, r := range s.recs {
		if r.File == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(s.dir, r.File)); err == nil {
			s.used += info.Size()
		}
	}
}

func (s *snapshotter) enabled() bool { return s != nil && s.dir != "" }

// capture 为这一步要动到的每个路径留一份写前快照。
//
// 路径**不在这里解析**：`types.PathAware.Paths` 是"这次调用涉及哪些路径"的 owner，
// 内部已走过工作区边界校验。这里只补一条：解析失败时它会原样回吐参数值
// （`tools/file/file.go:61`），所以只接受绝对路径——非绝对的那条工具自己也会拒，
// 没有副作用就没有可还原的东西。
//
// ctx 必须传下去：任务在自己的 worktree 里跑时，路径要按**本次任务**的边界解析。
// 用静态工作区解析出来的绝对路径是另一个文件——快照会存下一份没人动过的内容，
// 而真正被改的那份反而没有快照，还原就成了一句空话。
//
// 任何失败都**不影响执行**：快照是保险，不是前提。为了留后路而让任务跑不完，
// 是拿"能不能干活"去换"能不能回头"。
func (s *snapshotter) capture(ctx context.Context, stepID, toolName string, tool types.Tool, args map[string]any) {
	if !s.enabled() || tool == nil {
		return
	}
	pa, ok := tool.(types.PathAware)
	if !ok {
		return
	}
	for _, p := range pa.Paths(ctx, args) {
		if !filepath.IsAbs(p) {
			continue
		}
		s.one(stepID, toolName, p)
	}
}

// one 给一个路径记一条快照（内容能留就留）。
func (s *snapshotter) one(stepID, toolName, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := preImage{
		Seq: len(s.recs) + 1, StepID: stepID, Tool: toolName, Path: path,
		At: time.Now().Format(time.RFC3339),
	}
	info, err := os.Stat(path)
	if err != nil {
		// 不存在 = 这一步会**造出**新东西，还原它就是删掉它。这条也必须记。
		s.commit(rec)
		return
	}
	rec.Exists = true
	rec.Bytes = info.Size()
	rec.Mode = uint32(info.Mode().Perm())
	rec.ModTime = info.ModTime().Format(time.RFC3339)
	switch {
	case info.IsDir():
		rec.IsDir = true
		rec.Reason = "当时是个目录，内容未快照（还原只能删掉空目录）"
	case rec.Bytes > snapshotMaxBytes:
		rec.Reason = fmt.Sprintf("当时有 %s，超过单文件快照上限，内容未留存", humanSize(rec.Bytes))
	case s.used+rec.Bytes > snapshotMaxTotal:
		rec.Reason = "本任务快照总量已达上限，内容未留存"
	case isBinaryContent(path, rec.Bytes):
		rec.Reason = "二进制内容，未快照（只对文本做对比与还原）"
	default:
		name, n, werr := s.writeContent(rec.Seq, path)
		if werr != nil {
			rec.Reason = "读旧内容失败：" + types.Shorten(werr.Error(), 80)
		} else {
			rec.File = name
			rec.Bytes = n
			s.used += n
		}
	}
	s.commit(rec)
}

// commit 落一条记录并重写清单。调用方须持有 s.mu。
//
// 清单是**提交点**：内容文件先写、清单后写，所以崩溃最多留下一个没人引用的内容文件
// （随任务目录一起被裁剪），不会留下一条指向空文件的记录。
func (s *snapshotter) commit(rec preImage) {
	s.recs = append(s.recs, rec)
	if err := s.flushManifest(); err != nil {
		// 清单写不下去（磁盘满等）时这条快照对还原端就是隐形的。
		// 内存里仍然记着，所以本次任务的清单与 diff 照常可用——报一声，别静默。
		fmt.Fprintf(os.Stderr, "[gleam] 快照清单落盘失败（本次仍可用，重跑后不可还原）：%v\n", err)
	}
}

// writeContent 把 path 当前的内容复制进快照目录，返回文件名与字节数。
// 调用方须持有 s.mu（文件名跟着 seq 走，不能被别的步骤插队）。
func (s *snapshotter) writeContent(seq int, path string) (string, int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", 0, err
	}
	name := fmt.Sprintf("%d.pre", seq)
	// 原子写：这份文件的承诺是"写前的完整内容"，半截比缺失更坏——
	// 缺失会被读到，半截会被当成原文还原回去。
	if err := atomicfile.Write(filepath.Join(s.dir, name), b, 0o600); err != nil {
		return "", 0, err
	}
	return name, int64(len(b)), nil
}

// flushManifest 重写 manifest.json。调用方须持有 s.mu。
func (s *snapshotter) flushManifest() error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.recs, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.manifestPath, b, 0o600)
}

// records 返回本次任务的快照记录副本（塞进 Result，供改动清单使用）。
func (s *snapshotter) records() []preImage {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]preImage(nil), s.recs...)
}

// isBinaryContent 是不是"不该按文本对比"的内容：前 8KB 里有 NUL，或不是合法 UTF-8。
//
// 只探测开头一段：整份读进来就把体量上限的意义丢了，而真正的二进制文件在开头
// 几十字节内必然露出 NUL。UTF-8 校验放宽一档——缓冲区末尾可能正好切开一个
// 多字节字符，所以先去掉尾 4 字节再判一次。
func isBinaryContent(path string, size int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return true // 打不开就当二进制：不存内容，但路径与体量照常记
	}
	defer f.Close()
	buf := make([]byte, snapshotSniffLen)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	if strings.IndexByte(string(buf), 0) >= 0 {
		return true
	}
	if utf8.Valid(buf) {
		return false
	}
	if size <= snapshotSniffLen || len(buf) <= 4 {
		return true // 文件本来就完整读进来了，非法就是真非法
	}
	return !utf8.Valid(buf[:len(buf)-4])
}

// humanSize 快照原因里的人类可读体量。
//
// 与 cmd/gleam 的 humanBytes 同规则而不 import 它：那一份在 main 包里，
// 为一个字符串格式把整个状态报告逻辑跨包导出，代价大于收益。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	for _, u := range []string{"KB", "MB", "GB"} {
		v /= unit
		if v < unit {
			return fmt.Sprintf("%.1f %s", v, u)
		}
	}
	return fmt.Sprintf("%.1f GB", v)
}

// ---------- 读侧：改动清单、对比与还原都要按任务把快照取回来 ----------

// taskPreImages 读回某个任务的全部写前快照。
//
// taskID 先过 SafeTaskName（与任务归档读写同一条规则）：它来自 URL 路径参数，
// 而数据目录下有含密钥的文件，`..` 这类形状必须一刀挡掉。
// 没有快照目录不是错误——没动过文件的任务本来就没有快照，返回空。
func taskPreImages(dataDir, taskID string) ([]preImage, error) {
	root := SnapshotRoot(dataDir)
	if SafeTaskName(taskID) == "" || root == "" {
		return nil, fmt.Errorf("任务标识或数据目录不合法")
	}
	dir := filepath.Join(root, safeSeg(taskID))
	if _, err := toolutil.ResolveInRoots(filepath.Join(dir, manifestFileName), []string{root}); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, manifestFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var recs []preImage
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, fmt.Errorf("快照清单读不懂：%w", err)
	}
	return recs, nil
}

// pathKey 路径比较用的规范化形式。**只用来比，绝不用来读写**——它把分隔符统一成了
// 正斜杠、Windows 上还并成了小写，拿它去开文件是会出错的。
//
// 为什么要规范化：清单里的路径由工具解析写出，客户端传回的是同一串，但中间隔着一层
// JSON 与人手改的请求。不规范化就会出现"清单里明明有，却回答没有"，
// 用户看到的是一次自己没做错却被拒的还原。
func pathKey(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return filepath.ToSlash(p)
}

// firstPreImageOf 取某路径在本任务里的**第一条**快照。
//
// 同一路径被写三次时，用户点"还原"要的是"回到任务开始前的样子"，
// 不是"回到第 2 步之前的样子"，所以取最早那条。
func firstPreImageOf(recs []preImage, path string) (preImage, bool) {
	want := pathKey(path)
	for _, r := range recs {
		if r.Path != "" && pathKey(r.Path) == want {
			return r, true
		}
	}
	return preImage{}, false
}

// preImagePath 某条快照内容文件的绝对路径（过与读清单同一套边界校验）。
func preImagePath(dataDir, taskID string, rec preImage) (string, error) {
	if rec.File == "" {
		return "", fmt.Errorf("这条快照没有留住内容")
	}
	root := SnapshotRoot(dataDir)
	if SafeTaskName(taskID) == "" || root == "" {
		return "", fmt.Errorf("任务标识或数据目录不合法")
	}
	full := filepath.Join(root, safeSeg(taskID), filepath.Base(rec.File))
	if _, err := toolutil.ResolveInRoots(full, []string{root}); err != nil {
		return "", err
	}
	return full, nil
}

// preImageContent 读回某条快照存的内容。
func preImageContent(dataDir, taskID string, rec preImage) ([]byte, error) {
	full, err := preImagePath(dataDir, taskID, rec)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

// preImageGroup 还原这个路径时应当一起处理的路径集合。
//
// 绝大多数情况就是它自己。例外是 `file.move`：一次移动在清单里是两行（源端"没了"、
// 目标端"多出"），但它们是**同一个动作**。只退其中一行会把文件留在半路上——
// 还源端等于凭空删掉，还目标端等于凭空删掉整份内容。所以移动按组成对还原。
func preImageGroup(recs []preImage, path string) []preImage {
	first, ok := firstPreImageOf(recs, path)
	if !ok {
		return nil
	}
	if first.Tool != "file.move" {
		return []preImage{first}
	}
	var out []preImage
	seen := map[string]bool{}
	for _, r := range recs {
		if r.StepID != first.StepID || r.Tool != first.Tool || seen[pathKey(r.Path)] {
			continue
		}
		if head, has := firstPreImageOf(recs, r.Path); has {
			seen[pathKey(r.Path)] = true
			out = append(out, head)
		}
	}
	return out
}

// restorePreImage 把一个路径退回到这条快照记下的样子，返回给人的说明。
//
// 调用前必须先过 `preImage.reversible()`：不可还原的这条路径在这里没有第二条出路，
// 只能把原因如实报给用户，不能"尽力恢复一下"。
func restorePreImage(dataDir, taskID string, rec preImage) (string, error) {
	if !rec.Exists {
		info, err := os.Stat(rec.Path)
		if err != nil {
			return "本任务之前这里没有东西，现在也没有——无需还原", nil
		}
		if err := os.Remove(rec.Path); err != nil {
			if info.IsDir() {
				// 刻意用不递归的 Remove：目录非空说明里面还有不属于这次任务的东西，
				// 递归删下去就不是"还原"而是"顺手清盘"了。
				return "", fmt.Errorf("这个目录不是空的，没有删（里面可能有别的文件）：%w", err)
			}
			return "", err
		}
		if info.IsDir() {
			return "已删掉本任务建出的目录（仅空目录）", nil
		}
		return "已删掉本任务建出的文件", nil
	}
	b, err := preImageContent(dataDir, taskID, rec)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(rec.Path), 0o755); err != nil {
		return "", err
	}
	perm := os.FileMode(0o644)
	if rec.Mode != 0 {
		perm = os.FileMode(rec.Mode)
	}
	if err := atomicfile.Write(rec.Path, b, perm); err != nil {
		return "", err
	}
	return fmt.Sprintf("已退回到任务开始前的内容（%s）", humanSize(int64(len(b)))), nil
}
