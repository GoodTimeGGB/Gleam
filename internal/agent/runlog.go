package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gleam/pkg/types"
)

// RunLog 运行中的 append-only 步骤日志：<DataDir>/runs/<taskID>.jsonl。
//
// **为什么要有它。** 任务记录（`tasks/<id>.json`）是**跑完之后写一次的终态快照**
// （只有一个出口：`taskstore.go` 的 `SaveTaskResult`，CLI 与常驻服务都走它）。
// 进程若在运行中退出——用户关了窗口、机器重启、任务被强杀——那次运行**什么都没有**：
// 没有记录就没有回放、没有归因，连"跑到哪一步了"都答不上来。
// 审计的地基是**追加式日志**，不是终态快照；这一层补的就是中间那一段。
//
// **为什么用 JSONL 而不是一个 JSON 数组。** 追加写入必须是 O(1) 且不依赖读回旧内容：
// 边跑边重写整个数组，既慢，又会在崩溃时留下半个文件（连前面已经写好的部分都毁了）。
// 一行一条，坏了也只坏那一行。
//
// **生命周期：正常结束就删。** 任务记录一旦写成功，运行日志就是冗余的（终态快照更完整，
// 还带 Usage 与计划），于是 `Discard` 把它删掉——这样 `runs/` 里留下的**只有那些
// 没跑完的运行**，正好是唯一需要它的那批。不删的话这个目录会无界增长，
// 而"每个任务都留一份"带来的不是可追溯性，是没人看的噪音。
type RunLog struct {
	mu  sync.Mutex
	dir string
}

// NewRunLog 构造运行日志写入器。
func NewRunLog(dataDir string) *RunLog {
	return &RunLog{dir: filepath.Join(dataDir, "runs")}
}

// RecordStep 追加一条步骤记录（实现 StepSink）。
//
// 一律不返回错误：日志是治理地基，不是执行前提。为了留痕而让任务失败，
// 是拿"能不能跑完"换"能不能查"，这个交换不成立。
func (l *RunLog) RecordStep(taskID string, r types.StepResult) {
	if l == nil {
		return
	}
	name := SafeTaskName(taskID)
	if name == "" {
		return
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(l.dir, name+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// SafeTaskName 校验 taskID 能否直接作文件名；不安全就返回空串（调用方据此不写）。
//
// 为什么不"净化后照写"：把 `a/b` 与 `a_b` 映射到同一个文件，两次不同的运行会写进
// 同一份日志——那比不写更坏，因为读的人会以为看到的是完整的一次运行。
// 宁可不记，也不要记错。
//
// 这条规则**读写两侧共用**：任务归档的路径由 `TaskArchivePath` 按它拼，
// 读侧（webui / gleam replay / eval）也都走那一个入口——两处各写一份迟早漂移，
// 漂移的结果是"写得出去读不回来"。
func SafeTaskName(taskID string) string {
	if taskID == "" || len(taskID) > 128 {
		return ""
	}
	for _, r := range taskID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return ""
		}
	}
	return taskID
}

// RunLogPath 运行日志的路径（不保证存在）。
func RunLogPath(dataDir, taskID string) string {
	name := SafeTaskName(taskID)
	if name == "" {
		return ""
	}
	return filepath.Join(dataDir, "runs", name+".jsonl")
}

// ReadRunLog 读回一次运行中落盘的步骤，按写入顺序。
//
// 读不动就跳过那一行（半行 JSON 是崩溃现场的正常形态）：一份"缺最后一行"的日志
// 仍然能回答"跑到哪一步为止"，而整份拒绝解析等于把仅有的凭据也丢掉。
//
// **文件不存在不算错误。** 日志"正常结束就删"，所以不存在是绝大多数任务的状态、
// 是**预期**而不是异常；把它变成错误，等于让每个调用点都得先绕开一次预期中的失败，
// 而真正该被看见的读取错误反而淹没在同样的 err 里分不出来。返回空切片 + nil，
// 把"有没有留下半截运行"这件事留给调用方按内容判断。
func ReadRunLog(dataDir, taskID string) ([]types.StepResult, error) {
	path := RunLogPath(dataDir, taskID)
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []types.StepResult
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r types.StepResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// DiscardRunLog 删除一次运行的日志（任务记录写成功之后调用）。
func DiscardRunLog(dataDir, taskID string) {
	path := RunLogPath(dataDir, taskID)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
