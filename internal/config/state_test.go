package config_test

import (
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/filter"
)

func TestPersistentStateConfiguration(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(
		config.Options{File: writeFile(t, "config.yaml", minimalYAML), Environ: environ(), Overrides: map[string]any{
			"state.database":  "/var/lib/forgemirror/state.db",
			"source.base_url": "https://git.example.org/",
			"source.token":    "source-token",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PrivatePolicy() != filter.PrivateUnlessExcluded {
		t.Fatal("persistence changed publication policy")
	}
	if cfg.State.Database != "/var/lib/forgemirror/state.db" || !cfg.UsesSourceAPI() {
		t.Fatalf("state = %+v", cfg.State)
	}
}
