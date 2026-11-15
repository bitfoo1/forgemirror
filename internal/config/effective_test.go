package config_test

import (
	"strings"
	"testing"

	"github.com/knadh/koanf/parsers/yaml"

	"go.bitfoo.de/forgemirror/internal/config"
)

func TestListenersThatMayShareAnAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		// A disabled UI opens no port, so its address cannot collide.
		{name: "disabled ui", yaml: "ui:\n  listen_address: \":9090\"\n"},
		{
			name: "port zero",
			yaml: "server:\n  listen_address: \"127.0.0.1:0\"\n  ops_listen_address: \"127.0.0.1:0\"\n" +
				"ui:\n  enabled: true\n  listen_address: \"127.0.0.1:0\"\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := config.Load(config.Options{
				File: writeFile(t, "c.yaml", minimalYAML+tc.yaml), Environ: environ(),
			}); err != nil {
				t.Errorf("Load() error = %v", err)
			}
		})
	}
}

// typedTargetYAML appends a typed target to validYAML's target list; its
// secret is distinctive so that substring checks cannot match by accident.
const typedTargetYAML = `  - name: typed
    type: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token: typed-target-token
    settings:
      has_issues: false
      topics: [go]
`

func TestEffective(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(config.Options{
		File: writeFile(t, "c.yaml", strings.Replace(
			validYAML,
			"  token: source-token\n",
			"  token: source-token\n  base_url: https://git.example.org/\n",
			1,
		)+typedTargetYAML+"ui:\n  enabled: true\n"),
		Environ: environ(),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	eff := cfg.Effective()
	out, err := yaml.Parser().Marshal(eff)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(out)

	secrets := []string{cfg.Webhook.Secret.Reveal(), cfg.Source.Token.Reveal()}
	for _, tgt := range cfg.Targets {
		secrets = append(secrets, tgt.Token.Reveal())
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			t.Errorf("effective config contains secret %q:\n%s", s, text)
		}
	}

	for _, want := range []string{
		"listen_address: 127.0.0.1:9000",
		"ops_listen_address: :9090",
		"shutdown_timeout: 30s",
		"level: DEBUG",
		"token: '[REDACTED]'",
		"enabled: true",
		"has_issues: false",
		"name: codeberg",
		"- go",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("effective config lacks %q:\n%s", want, text)
		}
	}

	server, _ := eff["server"].(map[string]any)
	if _, ok := server["listen_address"].(string); !ok {
		t.Errorf("server.listen_address = %#v, want a string", server["listen_address"])
	}
}
