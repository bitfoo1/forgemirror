package ui_test

import (
	"net/http"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/plan"
)

func TestHistoryPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for range 21 {
		f.store.RunStarted(t.Context(), "acme/app", "", true)
		f.store.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true, Trigger: "manual sync"})
		f.store.RunFinished(t.Context(), "acme/app")
	}
	for _, tt := range []struct {
		path string
		code int
		text string
	}{
		{"/history?repo=acme%2Fapp", http.StatusOK, "Next page"},
		{"/history?repo=acme%2Fapp&page=2", http.StatusOK, "Previous page"},
		{"/history?repo=unknown", http.StatusNotFound, ""},
		{"/history?repo=acme%2Fapp&page=-1", http.StatusBadRequest, ""},
	} {
		rec := f.do(t, http.MethodGet, tt.path)
		if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.text) {
			t.Errorf("%s: %d %s", tt.path, rec.Code, rec.Body.String())
		}
	}
}
