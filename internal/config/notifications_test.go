package config_test

import (
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
)

func TestNotificationSecretsAndValidation(t *testing.T) {
	t.Parallel()
	file := writeFile(t, "notify-secret", "delivery-token")
	cfg, err := config.Load(
		config.Options{
			File:    writeFile(t, "config.yaml", minimalYAML),
			Environ: environ(),
			Overrides: map[string]any{
				"notifications.webhook_url": "https://hooks.example/secret-path",
				"notifications.token_file":  file,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notifications.Token.Reveal() != "delivery-token" {
		t.Fatal("notification token file not loaded")
	}
	effective := cfg.Effective()
	text := effective["notifications"].(map[string]any)
	if strings.Contains(text["webhook_url"].(string), "secret-path") ||
		strings.Contains(text["token"].(string), "delivery-token") {
		t.Fatal("notification secrets exposed")
	}
	for _, url := range []string{"ftp://example.com/", "https://user:secret@example.com/"} {
		_, err = config.Load(
			config.Options{
				File:      writeFile(t, "invalid.yaml", minimalYAML),
				Environ:   environ(),
				Overrides: map[string]any{"notifications.webhook_url": url},
			},
		)
		if err == nil {
			t.Errorf("invalid notification URL accepted: %s", url)
		}
	}
}
