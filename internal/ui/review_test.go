package ui_test

import (
	"net/http"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/plan"
)

func TestStaleComparisonWithoutAttemptIsNotInSync(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	recordPlan(t, f.store, plan.Repo{Name: "acme/app"})
	f.store.RunStarted(t.Context(), "acme/app", "", true)
	body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
	if strings.Contains(body, `title="GitHub: in sync`) ||
		!strings.Contains(body, `title="GitHub: awaiting verification`) {
		t.Fatalf("stale comparison is authoritative: %s", body)
	}
}

func TestFailedVerificationIsNotInSync(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	recordPlan(t, f.store, plan.Repo{Name: "acme/app", Apply: true})
	recordPlan(
		t,
		f.store,
		plan.Repo{Name: "acme/app", Targets: []plan.Target{{Name: "github", Error: "Could not verify refs"}}},
	)
	body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
	if !strings.Contains(body, `title="GitHub: error`) || !strings.Contains(body, "Could not verify") {
		t.Fatalf("verification failure hidden: %s", body)
	}
}

func TestCurrentDiffAndLastAppliedPlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	recordPlan(
		t,
		f.store,
		plan.Repo{
			Name:    "acme/app",
			At:      testNow(),
			Apply:   true,
			Targets: []plan.Target{{Name: "github", Refs: []plan.RefChange{{Ref: "refs/heads/removed", Old: "abc"}}}},
		},
	)
	recordPlan(t, f.store, plan.Repo{Name: "acme/app", At: testNow(), Targets: []plan.Target{{Name: "github"}}})
	body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
	for _, text := range []string{"Current diff", "Last applied plan", "refs/heads/removed", "succeeded", "no changes"} {
		if !strings.Contains(body, text) {
			t.Errorf("missing %q: %s", text, body)
		}
	}
	current := strings.Index(body, "Current diff")
	historical := strings.Index(body, "Last applied plan")
	ref := strings.Index(body, "refs/heads/removed")
	if current < 0 || historical <= current || ref <= historical {
		t.Fatal("applied change shown in current diff")
	}
}
