package app_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/app"
)

const testConfig = `
webhook:
  secret: s
log:
  format: text
targets:
  - name: example
    url: "https://example.com/{{ .FullName }}.git"
    token: x
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "forgemirror.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

type output struct {
	stdout, stderr string
}

// run executes the command with the given arguments and captures its output.
func run(t *testing.T, args ...string) (output, error) {
	t.Helper()

	var out, errOut bytes.Buffer
	cmd := app.NewCommand()
	cmd.Writer = &out
	cmd.ErrWriter = &errOut

	err := cmd.Run(t.Context(), append([]string{"forgemirror"}, args...))

	return output{stdout: out.String(), stderr: errOut.String()}, err
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	out, err := run(t, "--version")
	if err != nil {
		t.Fatalf("Run(--version) error = %v", err)
	}
	if !strings.Contains(out.stdout, "forgemirror version") {
		t.Errorf("--version output = %q, want it to contain %q", out.stdout, "forgemirror version")
	}
}

func TestServeLogsStartup(t *testing.T) {
	t.Parallel()

	out, err := run(t, "--config", writeConfig(t, testConfig), "--listen-address", "127.0.0.1:9999")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, want := range []string{"forgemirror starting", "listen_address=127.0.0.1:9999", "targets=1"} {
		if !strings.Contains(out.stderr, want) {
			t.Errorf("log output = %q, want it to contain %q", out.stderr, want)
		}
	}
}

func TestFlagOverridesLogging(t *testing.T) {
	t.Parallel()

	out, err := run(t, "-c", writeConfig(t, testConfig), "--log-format", "json", "--log-level", "error")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.stderr != "" {
		t.Errorf("log output = %q, want nothing at level error", out.stderr)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     string
		wantErr    string
		wantStdout string
	}{
		{name: "valid", config: testConfig, wantStdout: "configuration is valid (1 targets)"},
		{name: "invalid", config: "webhook:\n  secret: s\n", wantErr: "targets"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, "--config", writeConfig(t, tc.config), "validate")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Run(validate) error = %v, want it to contain %q", err, tc.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Run(validate) error = %v", err)
			}
			if !strings.Contains(out.stdout, tc.wantStdout) {
				t.Errorf("stdout = %q, want %q", out.stdout, tc.wantStdout)
			}
		})
	}
}

func TestServeConfigError(t *testing.T) {
	t.Parallel()

	_, err := run(t, "--config", filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Run() error = nil, want missing config file error")
	}
}
