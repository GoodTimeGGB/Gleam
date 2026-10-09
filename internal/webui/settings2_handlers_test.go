package webui

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gleam/pkg/types"
)

func TestParseSSHHostNames(t *testing.T) {
	cfg := `# comment
Host dev-box staging
  HostName 10.0.0.2
  IdentityFile ~/.ssh/id_ed25519
Host *
  ServerAliveInterval 30
host=prod # inline
Host !bad web-? "quoted"
Match host foo
Host dev-box
`
	got := parseSSHHostNames(bufio.NewScanner(strings.NewReader(cfg)))
	want := []string{"dev-box", "staging", "prod", "quoted"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRedactProxy(t *testing.T) {
	if got := redactProxy("http://user:pw@proxy:8080"); strings.Contains(got, "pw") || !strings.Contains(got, "proxy:8080") {
		t.Fatalf("not redacted: %s", got)
	}
	if got := redactProxy("proxy:3128"); got != "proxy:3128" {
		t.Fatalf("changed: %s", got)
	}
}

func TestGoalDeleteRemovesArchive(t *testing.T) {
	s, dataDir := newArchiveFixture(t)
	writeArchive(t, dataDir, "t-del", types.GoalResult{TaskID: "t-del", Goal: "x", Status: types.GoalSuccess, StartedAt: time.Now()})
	srv := newTokenTestServer(s) // 守卫要口令：走公共测试入口补上，别裸用 s.Handler()
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/goals/t-del", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("delete: %v %v", err, res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "tasks", "t-del.json")); !os.IsNotExist(err) {
		t.Fatalf("archive still on disk: %v", err)
	}
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/goals/t-del", nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 404 {
		t.Fatalf("second delete want 404 got %d", res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/goals/..%2Fsettings", nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 400 && res.StatusCode != 404 {
		t.Fatalf("traversal want 400/404 got %d", res.StatusCode)
	}
}
