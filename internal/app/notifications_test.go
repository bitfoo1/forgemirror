package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/notify"
)

func TestFailedSyncQueuesNotification(t *testing.T) {
	t.Parallel()
	events := make(chan notify.Event, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Error(err)
		}
		select {
		case events <- event:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	source := newForgeFake(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "git")
	script := strings.Replace(
		fakeGitScript(filepath.Join(dir, "git.log")),
		"case \"$1\" in",
		"case \"$1\" in\npush) exit 1 ;;",
		1,
	)
	writeExecutable(t, binary, script)
	configFile := writeConfig(t, testConfig+`
notifications:
  webhook_url: `+endpoint.URL+`
server:
  listen_address: "127.0.0.1:0"
  ops_listen_address: "127.0.0.1:0"
  shutdown_timeout: 5s
mirror:
  git_binary: `+binary+`
  work_dir: `+dir+`
`)
	service := startService(t.Context(), t, configFile)
	defer service.stop(t)
	postPush(t.Context(), t, service.addr, pushBody(source))
	select {
	case event := <-events:
		if event.Repo != "acme/app" || event.Target != "example" || event.State != "needs intervention" ||
			event.Reason != "sync_failed" {
			t.Fatalf("notification=%+v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed sync did not notify")
	}
}
