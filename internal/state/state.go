// Package state stores bounded, versioned status records in a single-writer SQLite database.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite" // Register the pure-Go driver for CGO-disabled releases.
)

const (
	schemaVersion   = 1
	privateFileMode = 0o600
)

// DB owns a database and its process-level lock. Each source has a separate namespace.
type DB struct {
	db    *sql.DB
	lock  *flock.Flock
	scope string
}

// Open opens or creates a private database. The lock is released by Close,
// including after a crash by the operating system, without stale lock recovery.
func Open(ctx context.Context, path, scope string) (*DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("state path: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	lock := flock.New(path+".lock", flock.SetPermissions(privateFileMode))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("locking state: %w", err)
	}
	if !locked {
		return nil, errors.New("state database already has an owner")
	}
	return openLocked(ctx, path, scope, lock)
}

func openLocked(ctx context.Context, path, scope string, lock *flock.Flock) (_ *DB, resultErr error) {
	defer func() {
		if resultErr != nil {
			_ = lock.Unlock()
		}
	}()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening state file: %w", err)
	}
	if err = file.Chmod(privateFileMode); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("securing state file: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("closing state file: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening state database: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if resultErr != nil {
			_ = db.Close()
		}
	}()
	if err = initialize(ctx, db); err != nil {
		return nil, err
	}
	return &DB{db: db, lock: lock, scope: scope}, nil
}

func initialize(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("reading state schema: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("unsupported state schema version %d", version)
	}
	for _, pragma := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configuring state database: %w", err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting state migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		"CREATE TABLE IF NOT EXISTS records (scope TEXT NOT NULL, name TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(scope,name))",
		"PRAGMA user_version=1",
	} {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrating state database: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing state migration: %w", err)
	}
	return nil
}

// Load returns only records belonging to the configured source identity.
func (d *DB) Load(ctx context.Context) (map[string][]byte, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT name,payload FROM records WHERE scope=?", d.scope)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	defer rows.Close()
	records := make(map[string][]byte)
	for rows.Next() {
		var name string
		var payload []byte
		if err = rows.Scan(&name, &payload); err != nil {
			return nil, fmt.Errorf("reading state: %w", err)
		}
		records[name] = payload
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading state rows: %w", err)
	}
	return records, nil
}

// Save atomically replaces one repository's complete status record.
func (d *DB) Save(ctx context.Context, name string, payload []byte) error {
	_, err := d.db.ExecContext(
		ctx,
		"INSERT INTO records(scope,name,payload) VALUES(?,?,?) ON CONFLICT(scope,name) DO UPDATE SET payload=excluded.payload",
		d.scope,
		name,
		payload,
	)
	if err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	return nil
}

// Close closes SQLite before releasing exclusive process ownership.
func (d *DB) Close() error {
	return errors.Join(d.db.Close(), d.lock.Unlock())
}
