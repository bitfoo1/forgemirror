package config_test

import (
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
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
