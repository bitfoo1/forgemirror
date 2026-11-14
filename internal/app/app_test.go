package app_test

import (
	"bytes"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/app"
)

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cmd := app.NewCommand()
	cmd.Writer = &out

	if err := cmd.Run(t.Context(), []string{"forgemirror", "--version"}); err != nil {
		t.Fatalf("Run(--version) error = %v", err)
	}
	if !strings.Contains(out.String(), "forgemirror version") {
		t.Errorf("--version output = %q, want it to contain %q", out.String(), "forgemirror version")
	}
}

func TestServeLogsStartup(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	cmd := app.NewCommand()
	cmd.ErrWriter = &errOut

	if err := cmd.Run(t.Context(), []string{"forgemirror"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(errOut.String(), "forgemirror starting") {
		t.Errorf("log output = %q, want startup message", errOut.String())
	}
}
