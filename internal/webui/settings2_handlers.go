package webui

import (
	"bufio"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gleam/internal/agent"
	"gleam/pkg/types"
)

// handleSSHHosts 列出 ~/.ssh/config（Windows 为 %USERPROFILE%\.ssh\config）里声明的主机别名。
//
// 只读 Host 行，只回别名：HostName / User / IdentityFile 等一概不解析、不回传，
// 更不会去碰 ~/.ssh 下的任何密钥文件。带通配符（* ?）或取反（!）的 Host 模式不是
// 一台具体的主机，跳过。Include 指令不跟进——跟进意味着去读别的文件，这里只看一个文件。
func (s *Server) handleSSHHosts(w http.ResponseWriter, _ *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"path": "", "exists": false, "hosts": []string{}})
		return
	}
	path := filepath.Join(home, ".ssh", "config")
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "exists": false, "hosts": []string{}})
		return
	}
	defer f.Close()
	hosts := parseSSHHostNames(bufio.NewScanner(f))
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "exists": true, "hosts": hosts})
}

// parseSSHHostNames 从 ssh_config 文本里挑出具体的 Host 别名（去重、保持出现顺序）。
func parseSSHHostNames(sc *bufio.Scanner) []string {
	seen := map[string]bool{}
	out := []string{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 关键字与参数之间可以是空白，也可以是 "="（ssh_config(5)）
		key, rest := line, ""
		if i := strings.IndexAny(line, " \t="); i >= 0 {
			key, rest = line[:i], strings.TrimLeft(line[i:], " \t=")
		}
		if !strings.EqualFold(key, "Host") {
			continue
		}
		if j := strings.Index(rest, "#"); j >= 0 {
			rest = rest[:j]
		}
		for _, name := range strings.Fields(rest) {
			name = strings.Trim(name, `"`)
			if name == "" || strings.ContainsAny(name, "*?!") || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// handleNetworkInfo 回答「Gleam 的出网走不走代理」：Go 的默认传输层按
// HTTPS_PROXY / HTTP_PROXY / NO_PROXY 环境变量选代理，这里如实报出进程看到的值。
// 代理地址里可能带账号口令，回传前去掉 userinfo。
func (s *Server) handleNetworkInfo(w http.ResponseWriter, _ *http.Request) {
	env := map[string]string{}
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
		v := os.Getenv(k)
		if v == "" {
			v = os.Getenv(strings.ToLower(k))
		}
		if v == "" {
			continue
		}
		if k != "NO_PROXY" {
			v = redactProxy(v)
		}
		env[k] = v
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	host := ""
	if u, err := url.Parse(s.Agent.Cfg.LLM.BaseURL); err == nil {
		host = u.Host
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proxy_env": env, "proxy_keys": keys,
		"llm_host": host, "llm_provider": s.Agent.Cfg.LLM.Provider,
	})
}

func redactProxy(v string) string {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		if i := strings.LastIndex(v, "@"); i >= 0 {
			return "***@" + v[i+1:]
		}
		return v
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}

// handleGoalDelete 删除一条已结束任务的记录：内存表里那份 + 盘上 tasks/<id>.json。
// 还在跑的任务不许删（先取消）；id 过不了归档名字规则的一律 400，不拼路径。
func (s *Server) handleGoalDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path := agent.TaskArchivePath(s.Agent.Cfg.DataDir, id)
	if path == "" {
		writeErr(w, http.StatusBadRequest, "非法任务 id")
		return
	}
	s.mu.Lock()
	t, inMem := s.tasks[id]
	if inMem && t.Status == types.GoalRunning {
		s.mu.Unlock()
		writeErr(w, http.StatusConflict, "任务还在运行，先取消再删除")
		return
	}
	if inMem {
		delete(s.tasks, id)
	}
	s.mu.Unlock()
	err := os.Remove(path)
	onDisk := err == nil
	if err != nil && !os.IsNotExist(err) {
		writeErr(w, http.StatusInternalServerError, "删除归档失败：%v", err)
		return
	}
	if !inMem && !onDisk {
		writeErr(w, http.StatusNotFound, "任务 %q 不存在", id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_id": id, "deleted": true})
}
