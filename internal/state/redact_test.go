package state_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/state"
)

func TestRedactorPreservesStructure(t *testing.T) {
	t.Parallel()
	redact := state.Redactor([]string{"plan", "s", "quote\"token"})
	data, err := redact(
		[]byte(
			`{"plan":{"name":"acme/app","error":"denied plan quote\"token","warnings":["https://alice:password@forge.example/repo"],"id":9007199254740993},"status":"succeeded"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record["plan"] == nil || string(record["status"]) != `"succeeded"` {
		t.Fatalf("structure corrupted: %s", data)
	}
	if strings.Contains(string(data), "password") || strings.Contains(string(record["plan"]), "quote") ||
		!strings.Contains(string(data), "9007199254740993") {
		t.Fatalf("unsafe or lossy payload: %s", data)
	}
}
