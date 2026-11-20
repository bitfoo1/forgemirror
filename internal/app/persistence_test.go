package app_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPersistentStartupFailure(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, []byte("broken database"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "--config", writeConfig(t, testConfig+`
state:
  database: `+path+`
source:
  base_url: https://git.example.org/
  token: source-token
`))
	if err == nil {
		t.Fatal("started with corrupt persistent state")
	}
}

func TestPersistenceRestoresBothViews(t *testing.T) {
	t.Parallel()
	f := newForgeFake(t)
	dir := t.TempDir()
	binary, _ := fakeGitBinary(t, dir)
	configFile := writeConfig(t, testConfig+`
source:
  base_url: `+f.srv.URL+`
  token: src-token
state:
  database: `+filepath.Join(dir, "state.db")+`
server:
  listen_address: "127.0.0.1:0"
  ops_listen_address: "127.0.0.1:0"
  shutdown_timeout: 5s
ui:
  enabled: true
  listen_address: "127.0.0.1:0"
mirror:
  git_binary: `+binary+`
  work_dir: `+dir+`
`)
	for i := range 2 {
		svc := startService(t.Context(), t, configFile)
		addr := waitForLog(t, svc.logs, listeningOn("web"))[1]
		waitForLog(t, svc.logs, regexp.MustCompile(`msg="plan: out of sync" repository=acme/app .*apply=false`))
		if i == 0 {
			postAction(t.Context(), t, addr, "http://"+addr)
			waitForLog(t, svc.logs, regexp.MustCompile(`msg="mirror finished" repository=acme/app`))
		} else {
			_, body := httpGet(t.Context(), t, "http://"+addr+"/fragments/repos")
			if !strings.Contains(body, "Last applied plan") || !strings.Contains(body, "Current diff") {
				t.Fatalf("history not restored: %s", body)
			}
		}
		svc.stop(t)
	}
}
