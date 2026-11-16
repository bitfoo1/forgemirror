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

func TestTargetRegistryHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, typ, url, registry string
		wantHost                 string
		wantInsecure             bool
		wantErr                  bool
	}{
		{name: "github.com", typ: "github", url: "https://github.com/a/{{ .Name }}.git", wantHost: "ghcr.io"},
		{
			name:     "gitlab.com",
			typ:      "gitlab",
			url:      "https://gitlab.com/a/{{ .Name }}.git",
			wantHost: "registry.gitlab.com",
		},
		{name: "codeberg", typ: "forgejo", url: "https://codeberg.org/a/{{ .Name }}.git", wantHost: "codeberg.org"},
		{
			name: "forgejo over http", typ: "forgejo", url: "http://127.0.0.1:3000/a/{{ .Name }}.git",
			wantHost: "127.0.0.1:3000", wantInsecure: true,
		},
		{name: "github enterprise", typ: "github", url: "https://ghe.example/a/{{ .Name }}.git", wantErr: true},
		{name: "self-hosted gitlab", typ: "gitlab", url: "https://gl.example/a/{{ .Name }}.git", wantErr: true},
		{
			name: "explicit host", typ: "github", url: "https://ghe.example/a/{{ .Name }}.git",
			registry: "containers.ghe.example", wantHost: "containers.ghe.example",
		},
		{
			name: "explicit http URL", typ: "gitlab", url: "https://gl.example/a/{{ .Name }}.git",
			registry: "http://reg.example:5000", wantHost: "reg.example:5000", wantInsecure: true,
		},
		{
			name: "explicit with path", typ: "gitlab", url: "https://gl.example/a/{{ .Name }}.git",
			registry: "reg.example/x", wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			yaml := "webhook:\n  secret: s\nsource:\n  base_url: https://git.example.org/\n  token: st\n" +
				"targets:\n  - name: t\n    type: " + tc.typ + "\n    url: \"" + tc.url + "\"\n    token: x\n"
			cfg, err := config.Load(config.Options{File: writeFile(t, "c.yaml", yaml), Environ: environ()})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			target := cfg.Targets[0]
			target.Registry = tc.registry

			host, insecure, err := target.RegistryHost()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("RegistryHost() = %q, want error", host)
				}

				return
			}
			if err != nil {
				t.Fatalf("RegistryHost() error = %v", err)
			}
			if host != tc.wantHost || insecure != tc.wantInsecure {
				t.Errorf("RegistryHost() = %q, %v; want %q, %v", host, insecure, tc.wantHost, tc.wantInsecure)
			}
		})
	}
}

func TestSourceRegistryHost(t *testing.T) {
	t.Parallel()

	host, insecure, err := config.Source{BaseURL: "http://127.0.0.1:3000/"}.RegistryHost()
	if err != nil || host != "127.0.0.1:3000" || !insecure {
		t.Errorf("RegistryHost() = %q, %v, %v; want 127.0.0.1:3000, true, nil", host, insecure, err)
	}
	if _, _, err = (config.Source{}).RegistryHost(); err == nil {
		t.Error("RegistryHost() without base_url: error = nil, want error")
	}
}
