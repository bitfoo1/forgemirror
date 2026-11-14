package config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
)

func TestSecretNeverPrints(t *testing.T) {
	t.Parallel()

	const value = "hunter2"
	s := config.Secret(value)
	holder := struct {
		Token config.Secret `json:"token"`
	}{Token: s}

	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("msg", "token", s, "holder", holder)

	jsonOut, err := json.Marshal(holder)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	outputs := map[string]string{
		"%v":   fmt.Sprintf("%v", s),
		"%+v":  fmt.Sprintf("%+v", holder),
		"%#v":  fmt.Sprintf("%#v", holder),
		"slog": logBuf.String(),
		"json": string(jsonOut),
	}
	for name, out := range outputs {
		if strings.Contains(out, value) {
			t.Errorf("%s output leaks the secret: %s", name, out)
		}
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("%s output = %s, want placeholder", name, out)
		}
	}

	if s.Reveal() != value {
		t.Errorf("Reveal() = %q, want %q", s.Reveal(), value)
	}
}

func TestEmptySecretPrintsEmpty(t *testing.T) {
	t.Parallel()

	if got := config.Secret("").String(); got != "" {
		t.Errorf("empty Secret String() = %q, want empty", got)
	}
}
