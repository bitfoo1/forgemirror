package ui_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHoldActionsValidateScopes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		values url.Values
		valid  bool
	}{
		{"all", url.Values{"all_targets": {"true"}, "all_repositories": {"true"}}, true},
		{"selected", url.Values{"targets": {"github"}, "repositories": {"acme/*"}, "duration": {"2h"}}, true},
		{"empty", url.Values{}, false},
		{"ambiguous targets", url.Values{"all_targets": {"true"}, "targets": {"github"}, "all_repositories": {"true"}}, false},
		{"invalid glob", url.Values{"targets": {"github"}, "repositories": {"acme/[a-z]"}}, false},
		{"invalid duration", url.Values{"all_targets": {"true"}, "all_repositories": {"true"}, "duration": {"-1h"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actions := &fakeActions{}
			f := newFixtureWith(t, nil, actions)
			rec := f.post(t, "/actions/hold", tc.values, map[string]string{"HX-Request": "true"})
			if tc.valid {
				if rec.Code != http.StatusOK || len(actions.recorded()) != 1 {
					t.Fatalf("valid hold: %d %v", rec.Code, actions.recorded())
				}
				if !strings.Contains(rec.Body.String(), "Current holds") {
					t.Fatal("controls fragment missing")
				}
			} else if rec.Code != http.StatusBadRequest || len(actions.recorded()) != 0 {
				t.Fatalf("invalid hold: %d %v", rec.Code, actions.recorded())
			}
		})
	}
}

func TestHoldActionsRespectPrefixAndBodyLimit(t *testing.T) {
	t.Parallel()
	actions := &fakeActions{}
	f := newFixtureWith(t, nil, actions)
	values := url.Values{"all_targets": {"true"}, "all_repositories": {"true"}}
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"http://example.test/ops/ui/actions/hold",
		strings.NewReader(values.Encode()),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	http.StripPrefix("/ops/ui", f.handler).ServeHTTP(rec, request)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "../" {
		t.Fatal("prefix-relative redirect lost")
	}
	for _, path := range []string{"/actions/hold", "/actions/resume-hold"} {
		response := f.post(t, path, url.Values{"reason": {strings.Repeat("secret-marker", 2000)}}, nil)
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "secret-marker") {
			t.Fatal("oversized hold request not safely rejected")
		}
	}
	if len(actions.recorded()) != 1 {
		t.Fatal("invalid request reached actions")
	}
}

func TestControlsAreSeparateAndProgressive(t *testing.T) {
	t.Parallel()
	f := newFixtureWith(t, reviewMode(), &fakeActions{})
	f.populate(t)
	body := f.do(t, http.MethodGet, "/").Body.String()
	controls := strings.Index(body, `id="controls"`)
	repos := strings.Index(body, `id="repos"`)
	if controls < 0 || controls >= repos {
		t.Fatal("controls not a separate card above repositories")
	}
	for _, want := range []string{"New hold", "Current holds", `value="until-resumed" selected`, `action="actions/hold"`, `data-control-draft`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if rec := f.do(t, http.MethodGet, "/fragments/controls"); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	rec := f.post(t, "/actions/hold", url.Values{"all_targets": {"true"}, "all_repositories": {"true"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "../" {
		t.Fatalf("native form: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}
