package ui_test

import (
	"net/http"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestPublicationReasonsAreVisible(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.store.PlanFinished(
		t.Context(),
		plan.Repo{
			Name:    "acme/app",
			Targets: []plan.Target{{Name: "github", ApprovalRequired: true, RefsUnknown: true}},
		},
	)
	if err := fixture.store.Pause(t.Context(), status.Pause{Target: "github", Reason: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	response := fixture.do(t, http.MethodGet, "/fragments/repos")
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	for _, want := range []string{`data-reason="publication_paused"`, `data-reason="approval_required"`, `data-reason="lfs_not_verified"`, "What happens next", "No successful target run retained", "destinations held"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
