package app_test

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func waitForApprovalIdentity(t *testing.T, web string) string {
	t.Helper()
	const pollInterval = 10 * time.Millisecond
	pattern := regexp.MustCompile(`name="identity" value="([a-f0-9]{64})"`)
	var body string
	for deadline := time.Now().Add(waitTimeout); time.Now().Before(deadline); time.Sleep(pollInterval) {
		_, body = httpGet(t.Context(), t, "http://"+web+"/fragments/repos")
		if match := pattern.FindStringSubmatch(body); len(match) == 2 {
			return match[1]
		}
	}
	t.Fatalf("approval identity missing after verification: %s", body)
	return ""
}

func TestFirstPublicationNeedsCurrentApproval(t *testing.T) {
	t.Parallel()
	source := newForgeFake(t)
	dir := t.TempDir()
	binary, _ := fakeGitBinary(t, dir)
	initial := typedConfigWith(t, source, binary, dir, "    first_publication_approval: true\n")
	contents, err := os.ReadFile(initial)
	if err != nil {
		t.Fatal(err)
	}
	configFile := writeConfig(t, string(contents)+`
ui:
  enabled: true
  listen_address: "127.0.0.1:0"
`)
	service := startService(t.Context(), t, configFile)
	defer service.stop(t)
	web := waitForLog(t, service.logs, listeningOn("web"))[1]
	postPush(t.Context(), t, service.addr, pushBody(source))
	waitForLog(t, service.logs, regexp.MustCompile(`msg="mirror finished with errors"`))
	if source.called("POST /api/v3/orgs/acme/repos") {
		t.Fatal("repository created before approval")
	}
	identity := waitForApprovalIdentity(t, web)
	form := url.Values{"repo": {"acme/app"}, "target": {"gh"}, "identity": {identity}}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"http://"+web+"/actions/approve",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Hx-Request", "true")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("approval status=%d", response.StatusCode)
	}
	waitForLog(t, service.logs, regexp.MustCompile(`msg="mirror finished"`))
	if !source.called("POST /api/v3/orgs/acme/repos") {
		t.Fatal("approved destination was not synchronized")
	}
}
