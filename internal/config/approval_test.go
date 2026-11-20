package config_test

import (
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
)

func TestFirstPublicationPolicy(t *testing.T) {
	t.Parallel()
	yaml := strings.Replace(
		minimalYAML,
		"    token: x",
		"    token: x\n    first_publication_approval: true\n    overrides:\n      - repos: acme/internal\n        first_publication_approval: false\n        settings: {}",
		1,
	)
	cfg, err := config.Load(
		config.Options{
			File:      writeFile(t, "config.yaml", yaml),
			Environ:   environ(),
			Overrides: map[string]any{"ui.enabled": true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Targets[0].RequiresFirstPublicationApproval("acme/app", false) ||
		cfg.Targets[0].RequiresFirstPublicationApproval("acme/internal", false) {
		t.Fatal("target/repository policy precedence incorrect")
	}
	if _, err = config.Load(
		config.Options{File: writeFile(t, "invalid.yaml", yaml), Environ: environ()},
	); err == nil ||
		!strings.Contains(err.Error(), "ui.enabled") {
		t.Fatalf("headless approval policy accepted: %v", err)
	}
}
