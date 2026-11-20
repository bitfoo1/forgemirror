package config_test

import (
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/filter"
)

func TestDatabaseIsMandatoryByDefault(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(config.Options{File: writeFile(t, "config.yaml", minimalYAML), Environ: environ()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.State.Database != "data/state.db" {
		t.Fatalf("database default=%q", cfg.State.Database)
	}
	if cfg.UsesSourceAPI() {
		t.Fatal("database forced API credentials on webhook-only operation")
	}
	for _, path := range []string{"", " ", "\t"} {
		_, err = config.Load(
			config.Options{
				File:      writeFile(t, "invalid.yaml", minimalYAML),
				Environ:   environ(),
				Overrides: map[string]any{"state.database": path},
			},
		)
		if err == nil || !strings.Contains(err.Error(), "state.database") {
			t.Errorf("empty database accepted: %q, %v", path, err)
		}
	}
}

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
