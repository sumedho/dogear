package dogear

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenWithOptionsAppliesPragmasToEveryConnection(t *testing.T) {
	store, err := OpenWithOptions(filepath.Join(t.TempDir(), "pooled.db"), StoreOptions{MaxOpenConns: 4, BusyTimeout: 1750 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var connections []interface{ Close() error }
	for range 4 {
		connection, err := store.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var foreignKeys, busyTimeout int
		if err := connection.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 1750 {
			t.Fatalf("connection pragmas: foreign_keys=%d busy_timeout=%d", foreignKeys, busyTimeout)
		}
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func TestPooledStoreSupportsConcurrentReadsAndWrites(t *testing.T) {
	store, err := OpenWithOptions(filepath.Join(t.TempDir(), "concurrent.db"), StoreOptions{MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errors := make(chan error, 12)
	for index := range 6 {
		wait.Add(2)
		go func(index int) {
			defer wait.Done()
			doc := Document{ID: fmt.Sprintf("doc-%d", index), Title: "Manual", SourcePath: "manual.md", SourceHash: fmt.Sprintf("hash-%d", index)}
			errors <- store.UpsertDocument(context.Background(), doc, []Chunk{{Ordinal: 1, Text: "content", TextHash: "text"}}, false)
		}(index)
		go func() {
			defer wait.Done()
			_, err := store.ListDocuments(context.Background())
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationFailureDoesNotAdvanceVersionOrClearFTS(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO chunks_fts(chunk_id, document_id, title, text) VALUES(1, 'doc', 'Manual', 'content'); DELETE FROM schema_migrations WHERE version = 5; ALTER TABLE documents RENAME TO documents_missing`); err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err == nil {
		t.Fatal("expected migration failure")
	}
	var version, indexed int
	if err := store.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM chunks_fts`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if version != 4 || indexed != 1 {
		t.Fatalf("failed migration left version=%d indexed=%d", version, indexed)
	}
}

func TestInitContextHonorsCancellation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "cancelled.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.InitContext(ctx); err == nil {
		t.Fatal("expected initialization to stop for a cancelled context")
	}
}
