package state_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"go.bitfoo.de/forgemirror/internal/state"
)

func openDB(t *testing.T, path, scope string) *state.DB {
	t.Helper()
	db, err := state.Open(t.Context(), path, scope)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDurableRecords(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "private", "state.db")
	db := openDB(t, path, "source-a")
	if err := db.Save(t.Context(), "acme/app", []byte(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	if other, err := state.Open(t.Context(), path, "source-a"); err == nil {
		_ = other.Close()
		t.Fatal("second writer accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openDB(t, path, "source-a")
	defer reopened.Close()
	records, err := reopened.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if string(records["acme/app"]) != `{"version":1}` {
		t.Fatalf("records = %s", records)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %v", info.Mode())
	}
}

func TestSourceIsolation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	db := openDB(t, path, "source-a")
	if err := db.Save(t.Context(), "repo", []byte("old source")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	other := openDB(t, path, "source-b")
	defer other.Close()
	records, err := other.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("wrong source records = %v", records)
	}
}

func futureDatabase(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.ExecContext(t.Context(), "PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectInvalidDatabase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
	}{
		{name: "corrupt", prepare: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "future", prepare: futureDatabase},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "state.db")
			tc.prepare(t, path)
			if db, err := state.Open(t.Context(), path, "source"); err == nil {
				_ = db.Close()
				t.Fatal("invalid database accepted")
			}
		})
	}
}

func TestCanceledWrite(t *testing.T) {
	t.Parallel()
	db := openDB(t, filepath.Join(t.TempDir(), "state.db"), "source")
	defer db.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := db.Save(ctx, "repo", []byte("x")); err == nil {
		t.Fatal("canceled write succeeded")
	}
}
