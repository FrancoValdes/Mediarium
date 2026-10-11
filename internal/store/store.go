// Package store owns the SQLite connection and schema migrations
// (one file covering config, library, indexer state, queue).
//
// The database runs in WAL mode with a small pool of connections: readers
// (every page the UI loads) never wait for the writer, and SQLite itself
// serialises the writers. Every write transaction starts with BEGIN
// IMMEDIATE, so two of them queue up for the write lock instead of failing
// halfway through. When WAL cannot be switched on (some network file
// systems), the database stays in the old rollback-journal mode and the pool
// shrinks to one connection, as before; Mode reports which one it is.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	migrations "github.com/rdborg/mediarium/db"
	_ "modernc.org/sqlite"
)

// poolSize is how many connections the pool may open in WAL mode. Small on
// purpose: a NAS has few cores, and SQLite allows one writer at a time anyway.
const poolSize = 8

// Journal modes reported by Mode.
const (
	ModeWAL      = "wal"
	ModeFallback = "delete" // rollback journal, one connection
)

// Info describes how a database was opened.
type Info struct {
	// JournalMode is ModeWAL, or ModeFallback when WAL could not be enabled.
	JournalMode string
	// WALProblem says why WAL could not be enabled ("" when it is on).
	WALProblem string
}

var (
	infoMu sync.Mutex
	infos  = map[*sql.DB]Info{}
)

// Mode reports how db was opened by Open. A database that Open did not
// create reports ModeWAL with no problem.
func Mode(db *sql.DB) Info {
	infoMu.Lock()
	defer infoMu.Unlock()
	if in, ok := infos[db]; ok {
		return in
	}
	return Info{JournalMode: ModeWAL}
}

func setInfo(db *sql.DB, in Info) {
	infoMu.Lock()
	infos[db] = in
	infoMu.Unlock()
}

// Open opens (creating if needed) the SQLite database at path and applies
// any pending migrations embedded from db/migrations (migrations.MigrationsFS).
func Open(path string) (*sql.DB, error) {
	conn, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	info := enableWAL(conn)
	if info.JournalMode == ModeWAL {
		conn.SetMaxOpenConns(poolSize)
		conn.SetMaxIdleConns(poolSize)
	} else {
		// The rollback journal lets a reader block the writer, so keep the
		// old rule of one connection at a time.
		conn.SetMaxOpenConns(1)
		log.Printf("database: WAL mode is not available here, using the older journal with one connection: %s", info.WALProblem)
	}
	setInfo(conn, info)

	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	restrictFiles(path)
	go watchPool(conn)
	return conn, nil
}

// restrictFiles makes the database, and its write-ahead log files, readable by
// the app's own user only. The database holds password hashes, the encrypted
// credentials and session records; the file next to it, secret.key, is already
// private, and the database should not be the weak one of the pair. Errors are
// ignored: some file systems (network shares, Windows) have no such modes.
func restrictFiles(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0o600)
		}
	}
}

// dsn builds the connection string. The pragmas run on every new connection.
func dsn(path string) string {
	// The path is part of a file: URI, so the characters that would start a
	// query or fragment are escaped.
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "journal_size_limit(67108864)")
	q.Set("_txlock", "immediate")
	return "file:" + escaped + "?" + q.Encode()
}

// enableWAL switches the database file to write-ahead logging. It never
// fails Open: if the file system cannot do it, the caller falls back.
func enableWAL(conn *sql.DB) Info {
	var mode string
	if err := conn.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
		return Info{JournalMode: ModeFallback, WALProblem: err.Error()}
	}
	if !strings.EqualFold(mode, "wal") {
		return Info{JournalMode: ModeFallback, WALProblem: fmt.Sprintf("SQLite stayed in %q mode", mode)}
	}
	return Info{JournalMode: ModeWAL}
}

func migrate(conn *sql.DB) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations.MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, filepath.Ext(name))
		var count int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&count); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if count > 0 {
			continue
		}
		sqlBytes, err := migrations.MigrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := applyMigration(conn, name, version, string(sqlBytes)); err != nil {
			return err
		}
	}
	return nil
}

// brokenLinks counts the rows whose foreign key points at nothing.
func brokenLinks(tx *sql.Tx) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&n)
	return n, err
}

// rebuildMarker starts a migration that rebuilds a table (create the new
// one, copy, drop the old, rename). Those run with foreign keys off, as
// SQLite asks, so dropping the old table doesn't delete or empty the rows
// that point at it; the keys are checked before the change is kept.
const rebuildMarker = "-- mediarium:rebuild"

func applyMigration(db *sql.DB, name, version, body string) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx) // the pragma is per connection: keep this one
	if err != nil {
		return fmt.Errorf("connect for %s: %w", name, err)
	}
	defer conn.Close()
	rebuild := strings.HasPrefix(body, rebuildMarker)
	if rebuild {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("prepare %s: %w", name, err)
		}
		defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", name, err)
	}
	// A rebuild must not leave a row pointing at nothing. An old database can
	// already have such rows somewhere else; those don't stop the upgrade,
	// only new ones do.
	brokenBefore := 0
	if rebuild {
		if brokenBefore, err = brokenLinks(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("check before %s: %w", name, err)
		}
	}
	if _, err := tx.Exec(body); err != nil {
		tx.Rollback()
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if rebuild {
		after, err := brokenLinks(tx)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("check %s: %w", name, err)
		}
		if after > brokenBefore {
			tx.Rollback()
			return fmt.Errorf("apply %s: it would leave rows pointing at nothing", name)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
		tx.Rollback()
		return fmt.Errorf("record %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	return nil
}
