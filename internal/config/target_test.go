package config_test

import (
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
)

func TestTargetRemoteURL(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(config.Options{File: writeFile(t, "c.yaml", validYAML), Environ: environ()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	r := repo.Repository{Owner: "dvonessen", Name: "forgemirror", FullName: "dvonessen/forgemirror"}
	tests := []struct {
		target int
		want   string
	}{
		{target: 0, want: "https://github.com/acme/forgemirror.git"},
		{target: 1, want: "https://codeberg.org/dvonessen/forgemirror.git"},
	}

	for _, tc := range tests {
		got, renderErr := cfg.Targets[tc.target].RemoteURL(r)
		if renderErr != nil {
			t.Fatalf("RemoteURL() error = %v", renderErr)
		}
		if got != tc.want {
			t.Errorf("RemoteURL() = %q, want %q", got, tc.want)
		}
	}
}

func TestTargetRemoteURLNotValidated(t *testing.T) {
	t.Parallel()

	target := config.Target{Name: "raw", URL: "https://example.com/{{ .Name }}"}
	if _, err := target.RemoteURL(repo.Repository{Name: "x"}); err == nil {
		t.Error("RemoteURL() on an unvalidated target: error = nil, want error")
	}
}

func TestTargetAddressAndLayers(t *testing.T) {
	t.Parallel()

	const yaml = `
webhook:
  secret: s
source:
  base_url: https://git.example.org/
  token: t
targets:
  - name: gh
    type: github
    url: "https://github.com/{{ .Owner }}/{{ .Name }}.git"
    token: x
    settings:
      has_issues: false
    overrides:
      - repos: "acme/app"
        settings:
          has_issues: true
      - repos: "acme/.*"
        settings:
          private: true
`
	cfg, err := config.Load(config.Options{File: writeFile(t, "c.yaml", yaml), Environ: environ()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Typed() {
		t.Error("Typed() = false, want true")
	}
	target := &cfg.Targets[0]

	addr, err := target.Address(repo.Repository{Owner: "acme", Name: "app", FullName: "acme/app"})
	if err != nil {
		t.Fatalf("Address() error = %v", err)
	}
	if want := (forge.Address{APIURL: "https://api.github.com", Namespace: "acme", Name: "app"}); addr != want {
		t.Errorf("Address() = %+v, want %+v", addr, want)
	}

	layers := target.Layers("acme/app")
	if len(layers) != 3 {
		t.Fatalf("Layers(acme/app) = %d layers, want 3", len(layers))
	}
	if layers[0].HasIssues == nil || *layers[0].HasIssues ||
		layers[1].HasIssues == nil || !*layers[1].HasIssues ||
		layers[2].Private == nil || !*layers[2].Private {
		t.Errorf("Layers(acme/app) wrong order or content: %+v", layers)
	}
	if got := len(target.Layers("bob/x")); got != 1 {
		t.Errorf("Layers(bob/x) = %d layers, want 1", got)
	}
	if got := len(target.Layers("acme/appendix")); got != 2 {
		t.Errorf("Layers(acme/appendix) = %d layers, want 2 (anchored match)", got)
	}
}

func TestTypedFalseWithoutTypes(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(config.Options{File: writeFile(t, "c.yaml", minimalYAML), Environ: environ()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Typed() {
		t.Error("Typed() = true, want false")
	}
}
