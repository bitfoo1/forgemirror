package ui_test

import (
	"net/http"
	"regexp"
	"testing"

	"go.bitfoo.de/forgemirror/internal/plan"
)

// Some browsers expose <summary> as a button, which strips the roles of its
// children; a heading inside it disappears from heading navigation.
func TestNoHeadingInsideSummary(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	summary := regexp.MustCompile(`(?s)<summary[^>]*>.*?</summary>`)
	heading := regexp.MustCompile(`<h[1-6][\s>]`)
	for _, path := range []string{"/", "/fragments/repos", "/fragments/controls"} {
		body := f.do(t, http.MethodGet, path).Body.String()
		for _, s := range summary.FindAllString(body, -1) {
			if heading.MatchString(s) {
				t.Errorf("%s: heading inside summary: %s", path, s)
			}
		}
	}
}

// Status must not depend on colour alone (WCAG 1.4.1): every repository row
// and pipeline item carries its state as text, and coloured badges carry a mark.
func TestStatusIsNotColourOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	recordPlan(t, f.store, plan.Repo{Name: "acme/app", At: testNow(), Targets: []plan.Target{
		{Name: "github", Refs: []plan.RefChange{{Ref: "refs/heads/main", Old: "abc", New: "def"}}},
	}})
	repos := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
	rows := regexp.MustCompile(`(?s)<li class="repo state-([a-z-]+)".*?<code class="repo-name">[^<]*</code>`+
		`<span class="visually-hidden">, ([^<]+)</span>`).FindAllStringSubmatch(repos, -1)
	if len(rows) == 0 {
		t.Fatalf("repository rows lack state text: %s", repos)
	}
	if rows[0][1] != "out-of-sync" || rows[0][2] != "out of sync" {
		t.Errorf("state class %q announced as %q", rows[0][1], rows[0][2])
	}
	if !regexp.MustCompile(`class="badge-mark" aria-hidden="true">~<`).MatchString(repos) {
		t.Error("out-of-sync badge lacks its ~ mark")
	}
	badge := regexp.MustCompile(`(?s)<span class="pill pill-(ok|warn|fail) forge-badge".*?</span></span>`)
	for _, b := range badge.FindAllStringSubmatch(repos, -1) {
		if !regexp.MustCompile(`class="badge-mark" aria-hidden="true">[✓~!]<`).MatchString(b[0]) {
			t.Errorf("%s badge lacks a status mark: %s", b[1], b[0])
		}
	}
	overview := f.do(t, http.MethodGet, "/fragments/overview").Body.String()
	items := regexp.MustCompile(`(?s)<li class="pl-[a-z]+" data-(?:trigger|target)=.*?</li>`).
		FindAllString(overview, -1)
	if len(items) == 0 {
		t.Fatalf("pipeline lacks trigger and target items: %s", overview)
	}
	for _, item := range items {
		if !regexp.MustCompile(`<span class="visually-hidden">: [a-z]`).MatchString(item) {
			t.Errorf("pipeline item lacks state text: %s", item)
		}
	}
}
