package ui_test

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestForgeBadgeStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		target        *plan.Target
		sourceError   string
		apply         bool
		alter         func(*testing.T, *status.Store)
		class, status string
	}{
		{name: "fresh match", target: &plan.Target{Name: "github"}, class: "ok", status: "in sync"},
		{
			name:   "pending changes",
			target: &plan.Target{Name: "github", Create: true},
			class:  "warn",
			status: "out of sync",
		},
		{
			name:   "comparison error",
			target: &plan.Target{Name: "github", Error: "cannot read refs"},
			class:  "fail",
			status: "error",
		},
		{name: "source error", sourceError: "source unavailable", class: "fail", status: "error"},
		{
			name:        "source error during sync",
			sourceError: "source unavailable",
			apply:       true,
			class:       "fail",
			status:      "error",
		},
		{name: "refresh in progress", target: &plan.Target{Name: "github"}, alter: func(t *testing.T, s *status.Store) {
			t.Helper()
			s.RunStarted(t.Context(), "acme/app", "", false)
		}, class: "muted", status: "refreshing"},
		{name: "no comparison", class: "muted", status: "not checked"},
		{
			name:   "unknown refs",
			target: &plan.Target{Name: "github", RefsUnknown: true},
			class:  "muted",
			status: "not verified",
		},
		{
			name:   "stale match",
			target: &plan.Target{Name: "github"},
			alter:  func(t *testing.T, s *status.Store) { t.Helper(); s.RunStarted(t.Context(), "acme/app", "", true) },
			class:  "muted",
			status: "awaiting verification",
		},
		{name: "failed push", target: &plan.Target{Name: "github"}, alter: func(t *testing.T, s *status.Store) {
			t.Helper()
			s.TargetFinished(t.Context(), "acme/app", "github", 0, errors.New("push denied"))
		}, class: "fail", status: "error"},
		{name: "failed operation", target: &plan.Target{Name: "github"}, alter: func(t *testing.T, s *status.Store) {
			t.Helper()
			s.SyncFinished(t.Context(), "acme/app", "github", "labels", errors.New("labels denied"))
		}, class: "fail", status: "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			p := plan.Repo{Name: "acme/app", At: testNow(), Error: tc.sourceError, Apply: tc.apply}
			if tc.target != nil {
				p.Targets = []plan.Target{*tc.target}
			}
			recordPlan(t, f.store, p)
			if tc.alter != nil {
				tc.alter(t, f.store)
			}
			body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
			checkGitHubBadge(t, body, tc.class, tc.status)
			if strings.Contains(body, `hold-summary:`) {
				t.Error("separate hold banner still rendered")
			}
		})
	}
}

func checkGitHubBadge(t *testing.T, body, class, state string) {
	t.Helper()
	heading := regexp.MustCompile(`(?s)<div class="repo-head".*?</div>`).FindString(body)
	badge := regexp.MustCompile(`(?s)<span[^>]*data-forge="github"[^>]*>.*?</span></span>`).FindString(heading)
	for _, want := range []string{
		`pill-` + class, `title="GitHub: ` + state, `<span class="visually-hidden">: ` + state,
	} {
		if !strings.Contains(badge, want) {
			t.Errorf("badge missing %q: %s", want, heading)
		}
	}
	// Status is text for assistive technology; a focusable, role-less span
	// would add a tab stop that does nothing.
	for _, unwanted := range []string{`tabindex=`, `aria-label=`} {
		if strings.Contains(badge, unwanted) {
			t.Errorf("badge contains %q: %s", unwanted, badge)
		}
	}
}

func TestMetadataGroupingPreservesDifferentPayloads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, needle string
		a, b         plan.Target
		rows         int
	}{
		{
			name:   "equal label values through different pointers",
			needle: "label <b>bug</b>",
			a: plan.Target{
				Labels: []plan.LabelChange{{Name: "bug", New: &forge.Label{Name: "bug", Color: "ff0000"}}},
			},
			b: plan.Target{
				Labels: []plan.LabelChange{{Name: "bug", New: &forge.Label{Name: "bug", Color: "ff0000"}}},
			},
			rows: 1,
		},
		{
			name:   "different label values",
			needle: "label <b>bug</b>",
			a:      plan.Target{Labels: []plan.LabelChange{{Name: "bug", New: &forge.Label{Color: "ff0000"}}}},
			b:      plan.Target{Labels: []plan.LabelChange{{Name: "bug", New: &forge.Label{Color: "00ff00"}}}},
			rows:   2,
		},
		{
			name:   "different release assets",
			needle: "release <b>v1</b>",
			a: plan.Target{
				Releases: []plan.ReleaseChange{
					{Tag: "v1", New: &forge.Release{Tag: "v1"}, AssetsAdded: []string{"a.zip"}},
				},
			},
			b: plan.Target{
				Releases: []plan.ReleaseChange{
					{Tag: "v1", New: &forge.Release{Tag: "v1"}, AssetsAdded: []string{"b.zip"}},
				},
			},
			rows: 2,
		},
		{
			name:   "equal releases",
			needle: "release <b>v1</b>",
			a:      plan.Target{Releases: []plan.ReleaseChange{{Tag: "v1", New: &forge.Release{Tag: "v1"}}}},
			b:      plan.Target{Releases: []plan.ReleaseChange{{Tag: "v1", New: &forge.Release{Tag: "v1"}}}},
			rows:   1,
		},
		{
			name:   "different package destinations",
			needle: "package <b>app</b>",
			a: plan.Target{
				Packages: []plan.PackageChange{
					{Package: "app", Image: "registry-a/app", Tags: []plan.TagChange{{Tag: "v1", New: "abc"}}},
				},
			},
			b: plan.Target{
				Packages: []plan.PackageChange{
					{Package: "app", Image: "registry-b/app", Tags: []plan.TagChange{{Tag: "v1", New: "abc"}}},
				},
			},
			rows: 2,
		},
		{
			name:   "equal packages",
			needle: "package <b>app</b>",
			a: plan.Target{
				Packages: []plan.PackageChange{
					{Package: "app", Image: "registry/app", Tags: []plan.TagChange{{Tag: "v1", New: "abc"}}},
				},
			},
			b: plan.Target{
				Packages: []plan.PackageChange{
					{Package: "app", Image: "registry/app", Tags: []plan.TagChange{{Tag: "v1", New: "abc"}}},
				},
			},
			rows: 1,
		},
		{
			name:   "unknown omitted refs are not shared",
			needle: "more ref",
			a:      plan.Target{RefsOmitted: 3},
			b:      plan.Target{RefsOmitted: 3},
			rows:   2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			tc.a.Name, tc.b.Name = "github", "backup"
			recordPlan(t, f.store, plan.Repo{Name: "acme/app", At: testNow(), Targets: []plan.Target{tc.a, tc.b}})
			body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
			rows := regexp.MustCompile(`(?s)<li class="c-[^"]*"[^>]*>.*?</li>`).FindAllString(body, -1)
			count := 0
			for _, row := range rows {
				if strings.Contains(row, tc.needle) {
					count++
				}
			}
			if count != tc.rows {
				t.Errorf("got %d rows containing %q, want %d", count, tc.needle, tc.rows)
			}
		})
	}
}

func TestCurrentDiffGroupsCompleteChanges(t *testing.T) {
	t.Parallel()
	f := newFixtureWith(t, reviewMode(), &fakeActions{})
	shared := plan.RefChange{Ref: "refs/heads/main", Old: "1111111aaaa", New: "2222222bbbb"}
	recordPlan(t, f.store, plan.Repo{Name: "acme/app", At: testNow(), Targets: []plan.Target{
		{
			Name:     "github",
			Refs:     []plan.RefChange{shared, {Ref: "refs/heads/other", Old: "abcdefg111", New: "2222222bbbb"}},
			Settings: []plan.FieldChange{{Field: "description", Old: "old", New: "new"}},
		},
		{
			Name: "backup",
			Refs: []plan.RefChange{
				shared,
				{Ref: "refs/heads/other", Old: "abcdefg222", New: "2222222bbbb"},
				{Ref: "refs/tags/obsolete", Old: "4444444"},
			},
			Settings: []plan.FieldChange{{Field: "description", Old: "old", New: "new"}},
		},
	}})
	body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
	rows := regexp.MustCompile(`(?s)<li class="c-[^"]*"[^>]*>.*?</li>`).FindAllString(body, -1)
	var mainRows, otherRows, settingRows int
	for _, row := range rows {
		switch {
		case strings.Contains(row, "refs/heads/main"):
			mainRows++
			if !strings.Contains(row, `data-forge="github"`) || !strings.Contains(row, `data-forge="backup"`) {
				t.Errorf("shared change lost target attribution: %s", row)
			}
		case strings.Contains(row, "refs/heads/other"):
			otherRows++
		case strings.Contains(row, "description:"):
			settingRows++
			if !strings.Contains(row, `data-forge="github"`) || !strings.Contains(row, `data-forge="backup"`) {
				t.Errorf("shared setting lost attribution: %s", row)
			}
		}
	}
	if mainRows != 1 || otherRows != 2 || settingRows != 1 {
		t.Errorf("rows: main=%d other=%d setting=%d; want 1,2,1", mainRows, otherRows, settingRows)
	}
	if !strings.Contains(body, "refs/tags/obsolete") {
		t.Error("target-specific deletion missing")
	}
}
