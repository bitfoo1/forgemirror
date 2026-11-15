package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
)

const validYAML = `
server:
  listen_address: "127.0.0.1:9000"
  shutdown_timeout: 30s
log:
  level: debug
  format: text
webhook:
  secret: hook-secret
source:
  token: source-token
mirror:
  timeout: 2m
  max_concurrent: 2
filter:
  require_topic: mirror
  include_branches: [main, "release/.*"]
targets:
  - name: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token: gh-token
  - name: codeberg
    url: "https://codeberg.org/{{ .Owner }}/{{ .Name }}.git"
    username: mirror-bot
    token: cb-token
`

// minimalYAML is the smallest valid configuration; everything else defaults.
const minimalYAML = `
webhook:
  secret: s
targets:
  - name: t
    url: "https://example.com/{{ .FullName }}.git"
    token: x
`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

func environ(vars ...string) func() []string {
	return func() []string { return vars }
}

func TestLoadValidFile(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(config.Options{File: writeFile(t, "c.yaml", validYAML), Environ: environ()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	checks := []struct {
		name      string
		got, want any
	}{
		{"listen address", cfg.Server.ListenAddress, "127.0.0.1:9000"},
		{"shutdown timeout", cfg.Server.ShutdownTimeout, 30 * time.Second},
		{"read header timeout default", cfg.Server.ReadHeaderTimeout, 10 * time.Second},
		{"log level", cfg.Log.Level, slog.LevelDebug},
		{"log format", cfg.Log.Format, config.LogFormatText},
		{"webhook secret", cfg.Webhook.Secret.Reveal(), "hook-secret"},
		{"max body default", cfg.Webhook.MaxBodyBytes, int64(5 << 20)},
		{"source username default", cfg.Source.Username, "oauth2"},
		{"source token", cfg.Source.Token.Reveal(), "source-token"},
		{"mirror timeout", cfg.Mirror.Timeout, 2 * time.Minute},
		{"mirror max concurrent", cfg.Mirror.MaxConcurrent, 2},
		{"git binary default", cfg.Mirror.GitBinary, "git"},
		{"filter topic", cfg.Filter.RequireTopic, "mirror"},
		{"filter branches", len(cfg.Filter.IncludeBranches), 2},
		{"target count", len(cfg.Targets), 2},
		{"target username default", cfg.Targets[0].Username, "oauth2"},
		{"target username explicit", cfg.Targets[1].Username, "mirror-bot"},
		{"target token", cfg.Targets[1].Token.Reveal(), "cb-token"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoadPrecedence(t *testing.T) {
	t.Parallel()

	file := writeFile(t, "c.yaml", minimalYAML+"server:\n  listen_address: \"file:1\"\n")

	tests := []struct {
		name      string
		env       []string
		overrides map[string]any
		want      string
	}{
		{name: "file beats default", want: "file:1"},
		{
			name: "env beats file",
			env:  []string{"FORGEMIRROR_SERVER__LISTEN_ADDRESS=env:2"},
			want: "env:2",
		},
		{
			name:      "override beats env",
			env:       []string{"FORGEMIRROR_SERVER__LISTEN_ADDRESS=env:2"},
			overrides: map[string]any{"server.listen_address": "flag:3"},
			want:      "flag:3",
		},
		{
			name: "unrelated and config-file env vars are ignored",
			env:  []string{"HOME=/root", "FORGEMIRROR_CONFIG=/etc/forgemirror.yaml"},
			want: "file:1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(config.Options{File: file, Environ: environ(tc.env...), Overrides: tc.overrides})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Server.ListenAddress != tc.want {
				t.Errorf("listen address = %q, want %q", cfg.Server.ListenAddress, tc.want)
			}
		})
	}
}

func TestLoadWithoutFile(t *testing.T) {
	t.Parallel()

	// Targets are a list and cannot come from env vars, so env-only config
	// fails validation, but the env values themselves must arrive.
	_, err := config.Load(config.Options{Environ: environ("FORGEMIRROR_WEBHOOK__SECRET=s")})
	if err == nil {
		t.Fatal("Load() error = nil, want missing targets error")
	}
	if strings.Contains(err.Error(), "webhook.secret") {
		t.Errorf("Load() error = %v, webhook.secret from env was not applied", err)
	}
	if !strings.Contains(err.Error(), "targets") {
		t.Errorf("Load() error = %v, want targets error", err)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		env  []string
		// wantErr lists substrings that must all appear in the error.
		wantErr []string
	}{
		{
			name:    "missing required fields are all reported",
			yaml:    "log:\n  level: info\n",
			wantErr: []string{"webhook.secret: required", "targets: at least one target is required"},
		},
		{
			name:    "empty targets list",
			yaml:    "webhook:\n  secret: s\ntargets: []\n",
			wantErr: []string{"targets: at least one target is required"},
		},
		{
			name:    "unknown key",
			yaml:    minimalYAML + "server:\n  listen_adress: \":1\"\n",
			wantErr: []string{"listen_adress"},
		},
		{
			name:    "unknown key from env",
			yaml:    minimalYAML,
			env:     []string{"FORGEMIRROR_SERVR__LISTEN_ADDRESS=:1"},
			wantErr: []string{"servr"},
		},
		{
			name:    "invalid log level",
			yaml:    minimalYAML + "log:\n  level: loud\n",
			wantErr: []string{"decoding config"},
		},
		{
			name:    "invalid filter pattern",
			yaml:    minimalYAML + "filter:\n  include_repos: \"(\"\n",
			wantErr: []string{"filter.include_repos"},
		},
		{
			name:    "source base URL not http",
			yaml:    minimalYAML + "source:\n  base_url: ssh://git.example/\n",
			wantErr: []string{"source.base_url: scheme must be https or http"},
		},
		{
			name:    "source base URL with query",
			yaml:    minimalYAML + "source:\n  base_url: https://git.example/?x=1\n",
			wantErr: []string{"source.base_url: must not contain a query or fragment"},
		},
		{
			name:    "source base URL with credentials",
			yaml:    minimalYAML + "source:\n  base_url: https://u:p@git.example/\n",
			wantErr: []string{"source.base_url: must not contain credentials"},
		},
		{
			name:    "invalid log format",
			yaml:    minimalYAML + "log:\n  format: xml\n",
			wantErr: []string{"log.format"},
		},
		{
			name:    "invalid listen address",
			yaml:    minimalYAML + "server:\n  listen_address: nonsense\n",
			wantErr: []string{"server.listen_address"},
		},
		{
			name: "non-positive values",
			yaml: `
targets:
  - name: t
    url: "https://example.com/{{ .Name }}"
    token: x
server:
  read_header_timeout: 0s
  shutdown_timeout: -1s
webhook:
  secret: s
  max_body_bytes: 0
mirror:
  timeout: 0s
  max_concurrent: 0
  git_binary: ""
`,
			wantErr: []string{
				"server.read_header_timeout", "server.shutdown_timeout", "webhook.max_body_bytes",
				"mirror.timeout", "mirror.max_concurrent", "mirror.git_binary",
			},
		},
		{
			name: "target problems",
			yaml: `
webhook:
  secret: s
targets:
  - url: "https://example.com/{{ .Name }}"
    token: x
  - name: dup
    url: "https://example.com/{{ .Name }}"
    token: x
  - name: dup
    url: "https://example.com/{{ .Name }}"
  - name: no-url
    token: x
  - name: bad-template
    url: "https://example.com/{{ .Name "
    token: x
  - name: unknown-field
    url: "https://example.com/{{ .Nope }}"
    token: x
  - name: ssh
    url: "git@github.com:acme/{{ .Name }}.git"
    token: x
  - name: creds
    url: "https://user:pass@example.com/{{ .Name }}"
    token: x
  - name: no-host
    url: "https:///{{ .Name }}"
    token: x
`,
			wantErr: []string{
				"targets[0].name: required",
				`targets[2].name: "dup" is already used by targets[1]`,
				"targets[2].token: required",
				"targets[3].url: required",
				"targets[4].url:",
				"targets[5].url:",
				"targets[6].url:",
				"targets[7].url: must not contain credentials",
				"targets[8].url: missing host",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(config.Options{File: writeFile(t, "c.yaml", tc.yaml), Environ: environ(tc.env...)})
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load() error = %v\nwant it to contain %q", err, want)
				}
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()

	_, err := config.Load(config.Options{File: filepath.Join(t.TempDir(), "absent.yaml"), Environ: environ()})
	if err == nil || !strings.Contains(err.Error(), "absent.yaml") {
		t.Errorf("Load() error = %v, want error naming the file", err)
	}
}

func TestSecretFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		return path
	}
	hook := write("hook", "file-hook-secret\n")
	src := write("src", "  file-source-token  ")
	tgt := write("tgt", "file-target-token")

	yaml := `
webhook:
  secret_file: ` + hook + `
source:
  token_file: ` + src + `
targets:
  - name: t
    url: "https://example.com/{{ .Name }}"
    token_file: ` + tgt + `
`
	cfg, err := config.Load(config.Options{File: writeFile(t, "c.yaml", yaml), Environ: environ()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Webhook.Secret.Reveal(); got != "file-hook-secret" {
		t.Errorf("webhook secret = %q", got)
	}
	if got := cfg.Source.Token.Reveal(); got != "file-source-token" {
		t.Errorf("source token = %q", got)
	}
	if got := cfg.Targets[0].Token.Reveal(); got != "file-target-token" {
		t.Errorf("target token = %q", got)
	}
}

func TestSecretFileErrors(t *testing.T) {
	t.Parallel()

	secretFile := writeFile(t, "secret", "x")
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "value and file",
			yaml:    "webhook:\n  secret: s\n  secret_file: " + secretFile + "\n",
			wantErr: "webhook.secret: set either the value or webhook.secret_file, not both",
		},
		{
			name:    "missing file",
			yaml:    "source:\n  token_file: /nonexistent/forgemirror/token\n",
			wantErr: "source.token_file",
		},
		{
			name: "target file missing",
			yaml: "webhook:\n  secret: s\ntargets:\n  - name: t\n    url: https://e.com/x\n" +
				"    token_file: /nonexistent/forgemirror/token\n",
			wantErr: "targets[0].token_file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(config.Options{File: writeFile(t, "c.yaml", tc.yaml), Environ: environ()})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Load() error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestFindFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := writeFile(t, "present.yaml", "")
	missing := filepath.Join(dir, "missing.yaml")

	tests := []struct {
		name       string
		candidates []string
		want       string
	}{
		{name: "first existing wins", candidates: []string{missing, existing}, want: existing},
		{name: "none exists", candidates: []string{missing}, want: ""},
		{name: "no candidates", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := config.FindFile(tc.candidates)
			if err != nil {
				t.Fatalf("FindFile() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("FindFile() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFindFileStatError(t *testing.T) {
	t.Parallel()

	// A path below a regular file fails with ENOTDIR, not ErrNotExist.
	notADir := writeFile(t, "file", "")
	if _, err := config.FindFile([]string{filepath.Join(notADir, "forgemirror.yaml")}); err == nil {
		t.Error("FindFile() error = nil, want stat error")
	}
}

func TestDefaultFiles(t *testing.T) {
	t.Parallel()

	files := config.DefaultFiles()
	if len(files) == 0 || files[0] != config.FileName {
		t.Errorf("DefaultFiles() = %v, want %q first", files, config.FileName)
	}
}
