package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/registry"
)

// artifactSource adds releases and packages to fakeSource.
type artifactSource struct {
	*fakeSource

	releases []forge.Release
	relErr   error
	packages []forge.Package
	pkgErr   error
}

func (s *artifactSource) Releases(context.Context, string, string) ([]forge.Release, error) {
	return slices.Clone(s.releases), s.relErr
}

// OpenAsset serves the asset name as its content.
func (s *artifactSource) OpenAsset(_ context.Context, a forge.Asset) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("content of " + a.Name)), nil
}

func (s *artifactSource) Packages(context.Context, string, string) ([]forge.Package, error) {
	return slices.Clone(s.packages), s.pkgErr
}

// releaseForge adds release support to fakeForge and records release calls.
type releaseForge struct {
	*fakeForge

	mu       sync.Mutex
	releases []forge.Release
	listErr  error
	ops      []string
}

func (f *releaseForge) opf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, fmt.Sprintf(format, args...))
}

func (f *releaseForge) opsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.ops)
}

func (f *releaseForge) ReleaseCapabilities() forge.ReleaseCapabilities {
	return forge.ReleaseCapabilities{Prerelease: true}
}

func (f *releaseForge) Releases(context.Context, forge.Address) ([]forge.Release, error) {
	f.opf("list")

	return slices.Clone(f.releases), f.listErr
}

func (f *releaseForge) CreateRelease(_ context.Context, _ forge.Address, r forge.Release) (forge.Release, error) {
	f.opf("create %s", r.Tag)

	return forge.Release{ID: 99, Tag: r.Tag}, nil
}

func (f *releaseForge) UpdateRelease(_ context.Context, _ forge.Address, current, desired forge.Release) error {
	f.opf("update %s(%d) name=%s", current.Tag, current.ID, desired.Name)

	return nil
}

func (f *releaseForge) DeleteRelease(_ context.Context, _ forge.Address, r forge.Release) error {
	f.opf("delete %s", r.Tag)

	return nil
}

func (f *releaseForge) UploadAsset(
	_ context.Context,
	_ forge.Address,
	r forge.Release,
	name string,
	size int64,
	body io.Reader,
) error {
	data, _ := io.ReadAll(body)
	f.opf("upload %s(%d)/%s size=%d %q", r.Tag, r.ID, name, size, data)

	return nil
}

func (f *releaseForge) DeleteAsset(_ context.Context, _ forge.Address, r forge.Release, a forge.Asset) error {
	f.opf("delete-asset %s/%s", r.Tag, a.Name)

	return nil
}

// fakeRegistry holds tag digests per repository and records copies.
type fakeRegistry struct {
	mu      sync.Mutex
	digests map[string]map[string]string
	copies  []string
	denied  []string
	copyErr error
}

func (r *fakeRegistry) Digests(
	_ context.Context,
	repo registry.Repository,
	tags []string,
	deniedIsMissing bool,
) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !deniedIsMissing {
		r.denied = append(r.denied, repo.String())
	}
	out := make(map[string]string)
	for _, t := range tags {
		if d, ok := r.digests[repo.String()][t]; ok {
			out[t] = d
		}
	}

	return out, nil
}

func (r *fakeRegistry) Copy(_ context.Context, src, dst registry.Repository, tag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.copyErr != nil {
		return r.copyErr
	}
	if src.Password.Reveal() != "src-token" || dst.Password.Reveal() != "gh-token" {
		return errors.New("wrong credentials")
	}
	r.copies = append(r.copies, src.String()+" -> "+dst.String()+":"+tag)

	return nil
}

func (r *fakeRegistry) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.copies)
}

const artifactTarget = `
  - name: github
    type: github
    url: "https://github.com/mirror/{{ .Name }}.git"
    token: gh-token
    prune_releases: true
    settings:
      labels: false
      releases: true
      packages: true
`

// tagRefs lists the same refs everywhere, so no push is needed and the
// release tags exist on the source.
func tagRefs(context.Context, mirror.Remote) (map[string]string, error) {
	return map[string]string{"refs/heads/main": "c1", "refs/tags/v1": "t1", "refs/tags/v2": "t2"}, nil
}

type artifactSetup struct {
	targets string
	src     *artifactSource
	forge   *releaseForge
	reg     *fakeRegistry
	auto    bool
	logger  *slog.Logger
}

func newArtifactDispatcher(t *testing.T, s artifactSetup) (*dispatch.Dispatcher, *recordingObserver) {
	t.Helper()

	obs := &recordingObserver{}
	logger := s.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	opts := dispatch.Options{
		Mirrorer:        &fakeMirrorer{refsHook: tagRefs},
		Source:          config.Source{BaseURL: "https://git.example", Username: "oauth2", Token: "src-token"},
		Targets:         loadTargets(t, s.targets),
		ForgeSource:     s.src,
		Forges:          map[string]forge.Target{"github": s.forge},
		MaxConcurrent:   4,
		Auto:            s.auto,
		Logger:          logger,
		Observer:        obs,
		TransferTimeout: time.Minute,
	}
	if s.reg != nil {
		opts.Registry = s.reg
	}

	return dispatch.New(opts), obs
}

func day(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }

func newArtifactSource() *artifactSource {
	return &artifactSource{fakeSource: sourceRepo()}
}

func existingForge() *releaseForge {
	return &releaseForge{fakeForge: &fakeForge{repo: forge.Repo{
		Description: "the app", DefaultBranch: "main", Topics: []string{"go"}, HasIssues: true,
	}}}
}

func targetPlan(t *testing.T, obs *recordingObserver) plan.Target {
	t.Helper()

	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.plans) == 0 || len(obs.plans[0].Targets) == 0 {
		t.Fatalf("no target plan recorded: %+v", obs.plans)
	}

	return obs.plans[0].Targets[0]
}

func TestReleaseSync(t *testing.T) {
	t.Parallel()

	src := newArtifactSource()
	src.releases = []forge.Release{
		{Tag: "v2", Name: "Two", CreatedAt: day(2), Assets: []forge.Asset{{Name: "b.tgz", Size: 2}}},
		{Tag: "v1", Name: "One (new)", CreatedAt: day(1), Assets: []forge.Asset{
			{Name: "same", Size: 1}, {Name: "grown", Size: 9}, {Name: "added", Size: 3},
		}},
		{Tag: "v3", Name: "draft", Draft: true, CreatedAt: day(3)},
		{Tag: "v4", Name: "tag not pushed", CreatedAt: day(4)},
	}
	gh := existingForge()
	gh.releases = []forge.Release{
		{ID: 1, Tag: "v1", Name: "One", Assets: []forge.Asset{
			{ID: 11, Name: "same", Size: 1}, {ID: 12, Name: "grown", Size: 1}, {ID: 13, Name: "gone", Size: 1},
		}},
		{ID: 5, Tag: "v0", Name: "removed on source"},
	}
	d, obs := newArtifactDispatcher(t, artifactSetup{targets: artifactTarget, src: src, forge: gh, auto: true})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	want := []string{
		"list",
		"update v1(1) name=One (new)",
		"delete-asset v1/grown",
		"delete-asset v1/gone",
		// Uploads follow the source's asset order.
		`upload v1(1)/grown size=9 "content of grown"`,
		`upload v1(1)/added size=3 "content of added"`,
		"create v2",
		`upload v2(99)/b.tgz size=2 "content of b.tgz"`,
		"delete v0",
	}
	if got := gh.opsSnapshot(); !slices.Equal(got, want) {
		t.Fatalf("release calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	p := targetPlan(t, obs)
	if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "release v4 skipped") }) {
		t.Errorf("warnings = %v, want the release without a source tag reported", p.Warnings)
	}
	_, syncs := obs.snapshot()
	if !slices.ContainsFunc(syncs, func(s syncResult) bool { return s.operation == "releases" && s.err == nil }) {
		t.Errorf("syncs = %+v, want a successful releases operation", syncs)
	}
}

func TestReleaseSyncNeverPrunesOnFailedListing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*artifactSource, *releaseForge)
		warn  string
	}{
		{
			name:  "source listing fails",
			setup: func(s *artifactSource, _ *releaseForge) { s.relErr = errors.New("source down") },
			warn:  "listing source releases failed: source down",
		},
		{
			name:  "target listing fails",
			setup: func(_ *artifactSource, f *releaseForge) { f.listErr = errors.New("target down") },
			warn:  "listing releases failed: target down",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := newArtifactSource()
			gh := existingForge()
			gh.releases = []forge.Release{{ID: 5, Tag: "v0"}}
			tc.setup(src, gh)
			d, obs := newArtifactDispatcher(t, artifactSetup{targets: artifactTarget, src: src, forge: gh, auto: true})

			d.Dispatch(t.Context(), event("app"))
			shutdown(t, d)

			for _, op := range gh.opsSnapshot() {
				if op != "list" {
					t.Errorf("release call %q after a failed listing, want none", op)
				}
			}
			if p := targetPlan(t, obs); !slices.Contains(p.Warnings, tc.warn) || len(p.Releases) != 0 {
				t.Errorf("plan = %+v, want warning %q and no release changes", p, tc.warn)
			}
		})
	}
}

func TestReleasesOffByDefault(t *testing.T) {
	t.Parallel()

	src := newArtifactSource()
	src.releases = []forge.Release{{Tag: "v1"}}
	gh := existingForge()
	d, _ := newArtifactDispatcher(t, artifactSetup{targets: githubTarget, src: src, forge: gh, auto: true})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if ops := gh.opsSnapshot(); len(ops) != 0 {
		t.Errorf("release calls = %v, want none without settings.releases", ops)
	}
}

func TestReleasesOnlyPlannedInReviewMode(t *testing.T) {
	t.Parallel()

	src := newArtifactSource()
	src.releases = []forge.Release{{Tag: "v1", Name: "One"}}
	gh := existingForge()
	d, obs := newArtifactDispatcher(t, artifactSetup{targets: artifactTarget, src: src, forge: gh})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if ops := gh.opsSnapshot(); !slices.Equal(ops, []string{"list"}) {
		t.Errorf("release calls = %v, want only the listing", ops)
	}
	if p := targetPlan(t, obs); len(p.Releases) != 1 || p.Releases[0].Kind() != plan.KindCreate {
		t.Errorf("plan releases = %+v, want one creation", p.Releases)
	}
}

func packageSource() *artifactSource {
	src := newArtifactSource()
	src.packages = []forge.Package{
		{Name: "app", Type: "container", Tags: []string{"1.0.0", "latest", "sha256-d1.sig"}},
		{Name: "charts/app", Type: "container", Tags: []string{"0.1.0"}},
		{Name: "blob", Type: "generic", Tags: []string{"1"}},
	}

	return src
}

func packageRegistry() *fakeRegistry {
	return &fakeRegistry{digests: map[string]map[string]string{
		"git.example/acme/app":        {"1.0.0": "d1", "latest": "d1", "sha256-d1.sig": "s1"},
		"git.example/acme/charts/app": {"0.1.0": "c1"},
		"ghcr.io/mirror/app":          {"1.0.0": "d1", "latest": "d0"},
		"ghcr.io/mirror/charts/app":   {"0.1.0": "c1"},
	}}
}

func TestPackageSync(t *testing.T) {
	t.Parallel()

	reg := packageRegistry()
	d, obs := newArtifactDispatcher(t, artifactSetup{
		targets: artifactTarget, src: packageSource(), forge: existingForge(), reg: reg, auto: true,
	})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	want := []string{
		"git.example/acme/app -> ghcr.io/mirror/app:latest",
		"git.example/acme/app -> ghcr.io/mirror/app:sha256-d1.sig",
	}
	if got := reg.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("copies = %v, want %v", got, want)
	}
	p := targetPlan(t, obs)
	if len(p.Packages) != 1 || p.Packages[0].Image != "ghcr.io/mirror/app" {
		t.Errorf("plan packages = %+v", p.Packages)
	}
	if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "package blob skipped") }) {
		t.Errorf("warnings = %v, want the generic package reported", p.Warnings)
	}
	if reg.denied == nil || slices.Contains(reg.denied, "ghcr.io/mirror/app") {
		t.Errorf("strict digest reads = %v, want only source repositories", reg.denied)
	}
	_, syncs := obs.snapshot()
	if !slices.ContainsFunc(syncs, func(s syncResult) bool { return s.operation == "packages" && s.err == nil }) {
		t.Errorf("syncs = %+v, want a successful packages operation", syncs)
	}
}

func TestPackagesSkippedForPrivateRepositories(t *testing.T) {
	t.Parallel()

	src := packageSource()
	src.repo.Private = true
	reg := packageRegistry()
	d, obs := newArtifactDispatcher(t, artifactSetup{
		targets: artifactTarget, src: src, forge: existingForge(), reg: reg, auto: true,
	})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if got := reg.snapshot(); len(got) != 0 {
		t.Errorf("copies = %v, want none for a private repository", got)
	}
	if p := targetPlan(t, obs); !slices.Contains(p.Warnings, "packages are not synced for private repositories") {
		t.Errorf("warnings = %v", p.Warnings)
	}
}

func TestPackageSyncFailures(t *testing.T) {
	t.Parallel()

	t.Run("source listing fails", func(t *testing.T) {
		t.Parallel()

		src := packageSource()
		src.pkgErr = errors.New("boom")
		reg := packageRegistry()
		d, obs := newArtifactDispatcher(
			t,
			artifactSetup{targets: artifactTarget, src: src, forge: existingForge(), reg: reg, auto: true},
		)

		d.Dispatch(t.Context(), event("app"))
		shutdown(t, d)

		if len(reg.snapshot()) != 0 ||
			!slices.Contains(targetPlan(t, obs).Warnings, "listing source packages failed: boom") {
			t.Errorf("copies = %v, plan = %+v", reg.snapshot(), targetPlan(t, obs))
		}
	})

	t.Run("copy fails", func(t *testing.T) {
		t.Parallel()

		reg := packageRegistry()
		reg.copyErr = errors.New("denied")
		d, obs := newArtifactDispatcher(t, artifactSetup{
			targets: artifactTarget, src: packageSource(), forge: existingForge(), reg: reg, auto: true,
		})

		d.Dispatch(t.Context(), event("app"))
		shutdown(t, d)

		_, syncs := obs.snapshot()
		if !slices.ContainsFunc(syncs, func(s syncResult) bool { return s.operation == "packages" && s.err != nil }) {
			t.Errorf("syncs = %+v, want a failed packages operation", syncs)
		}
	})

	t.Run("no registry client", func(t *testing.T) {
		t.Parallel()

		d, obs := newArtifactDispatcher(t, artifactSetup{
			targets: artifactTarget, src: packageSource(), forge: existingForge(), auto: true,
		})

		d.Dispatch(t.Context(), event("app"))
		shutdown(t, d)

		if p := targetPlan(t, obs); !slices.ContainsFunc(p.Warnings, func(w string) bool {
			return strings.Contains(w, "no registry client")
		}) {
			t.Errorf("warnings = %v", p.Warnings)
		}
	})
}

func TestGitLabPackagePaths(t *testing.T) {
	t.Parallel()

	const gitlabArtifacts = `
  - name: github
    type: gitlab
    url: "https://gitlab.com/group/{{ .Name }}.git"
    token: gh-token
    settings:
      packages: true
`
	reg := &fakeRegistry{digests: map[string]map[string]string{
		"git.example/acme/app":        {"1.0.0": "d1"},
		"git.example/acme/charts/app": {"0.1.0": "c1"},
	}}
	d, _ := newArtifactDispatcher(t, artifactSetup{
		targets: gitlabArtifacts, src: packageSource(), forge: existingForge(), reg: reg, auto: true,
	})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	want := []string{
		"git.example/acme/app -> registry.gitlab.com/group/app:1.0.0",
		"git.example/acme/charts/app -> registry.gitlab.com/group/app/charts/app:0.1.0",
	}
	if got := reg.snapshot(); !slices.Equal(got, want) {
		t.Errorf("copies = %v, want %v", got, want)
	}
}

func TestReleaseChangeLog(t *testing.T) {
	t.Parallel()

	var buf syncBuffer
	src := packageSource()
	src.releases = []forge.Release{{Tag: "v1", Name: "One", Assets: []forge.Asset{{Name: "a"}}}}
	d, _ := newArtifactDispatcher(t, artifactSetup{
		targets: artifactTarget, src: src, forge: existingForge(), reg: packageRegistry(), auto: true,
		logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	out := buf.String()
	for _, want := range []string{
		`msg="release created"`, "target=github tag=v1 fields=[] assets_added=[a]",
		`msg="package tags copied"`, "package=app image=ghcr.io/mirror/app count=2",
		`msg="plan: release create"`, `msg="plan: package tags"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "src-token") || strings.Contains(out, "gh-token") {
		t.Error("log contains a token")
	}
}
