package dogear

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

type Store struct {
	db   *sql.DB
	path string
}

type StoreOptions struct {
	MaxOpenConns int
	BusyTimeout  time.Duration
}

func Open(path string) (*Store, error) {
	return OpenWithOptions(path, StoreOptions{MaxOpenConns: 1, BusyTimeout: 5 * time.Second})
}

func OpenWithOptions(path string, options StoreOptions) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if options.MaxOpenConns <= 0 {
		options.MaxOpenConns = 1
	}
	if options.BusyTimeout <= 0 {
		options.BusyTimeout = 5 * time.Second
	}
	dsn, err := storeDSN(path, options.BusyTimeout)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(options.MaxOpenConns)
	db.SetMaxIdleConns(options.MaxOpenConns)
	store := &Store{db: db, path: path}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func storeDSN(path string, busyTimeout time.Duration) (string, error) {
	var parsed *url.URL
	var err error
	if strings.HasPrefix(path, "file:") {
		parsed, err = url.Parse(path)
	} else {
		absolute, absErr := filepath.Abs(path)
		if absErr != nil {
			return "", absErr
		}
		parsed = &url.URL{Scheme: "file", Path: absolute}
	}
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	query.Set("_txlock", "immediate")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
