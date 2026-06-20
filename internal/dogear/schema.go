package dogear

import (
	"context"
	"database/sql"
)

const schemaVersion = 5

type migration struct {
	version int
	up      func(context.Context, *sql.Tx) error
}

func (s *Store) Init() error {
	return s.InitContext(context.Background())
}

// InitContext initializes and migrates the store while honoring cancellation.
func (s *Store) InitContext(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for _, item := range s.migrations() {
		if item.version <= current {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := item.up(ctx, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, item.version, now()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current = item.version
	}
	return s.verifyFTS5()
}

func (s *Store) migrations() []migration {
	return []migration{
		{version: 1, up: func(ctx context.Context, tx *sql.Tx) error {
			statements := []string{
				`CREATE TABLE IF NOT EXISTS documents (
					id TEXT PRIMARY KEY, title TEXT NOT NULL, brand TEXT, model TEXT, version TEXT,
					source_path TEXT NOT NULL, source_hash TEXT NOT NULL, tags_json TEXT NOT NULL DEFAULT '[]',
					created_at TEXT NOT NULL, updated_at TEXT NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS chunks (
					id INTEGER PRIMARY KEY, document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
					ordinal INTEGER NOT NULL, heading_path TEXT, heading_level INTEGER, page_number INTEGER,
					start_line INTEGER, end_line INTEGER, text TEXT NOT NULL, text_hash TEXT NOT NULL,
					UNIQUE(document_id, ordinal)
				)`,
				`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
					chunk_id UNINDEXED, document_id UNINDEXED, title, brand, model, heading_path, text,
					tokenize='unicode61'
				)`,
				`CREATE INDEX IF NOT EXISTS idx_chunks_document_id ON chunks(document_id)`,
				`CREATE INDEX IF NOT EXISTS idx_chunks_page ON chunks(document_id, page_number)`,
				`CREATE INDEX IF NOT EXISTS idx_documents_source_hash ON documents(source_hash)`,
			}
			return execStatements(ctx, tx, statements)
		}},
		{version: 2, up: func(ctx context.Context, tx *sql.Tx) error {
			return execStatements(ctx, tx, []string{
				`CREATE TABLE IF NOT EXISTS document_images (
					id INTEGER PRIMARY KEY, document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
					chunk_id INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE, ordinal INTEGER NOT NULL,
					alt_text TEXT NOT NULL, media_type TEXT NOT NULL, data BLOB NOT NULL, content_hash TEXT NOT NULL,
					UNIQUE(document_id, ordinal)
				)`,
				`CREATE INDEX IF NOT EXISTS idx_document_images_chunk_id ON document_images(chunk_id)`,
			})
		}},
		{version: 3, up: func(ctx context.Context, tx *sql.Tx) error {
			return execStatements(ctx, tx, []string{`CREATE TABLE IF NOT EXISTS embedding_index_state (
				id INTEGER PRIMARY KEY CHECK(id = 1), model TEXT NOT NULL, dimensions INTEGER NOT NULL,
				config_hash TEXT NOT NULL, complete INTEGER NOT NULL DEFAULT 0,
				indexed_count INTEGER NOT NULL DEFAULT 0, total_count INTEGER NOT NULL DEFAULT 0,
				updated_at TEXT NOT NULL
			)`})
		}},
		{version: 4, up: func(ctx context.Context, tx *sql.Tx) error {
			hasWarnings, err := hasColumn(ctx, tx, "documents", "import_warnings_json")
			if err != nil || hasWarnings {
				return err
			}
			_, err = tx.ExecContext(ctx, `ALTER TABLE documents ADD COLUMN import_warnings_json TEXT NOT NULL DEFAULT '[]'`)
			return err
		}},
		{version: 5, up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := rebuildIndexWith(ctx, tx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE embedding_index_state SET complete = 0`)
			return err
		}},
	}
}

func execStatements(ctx context.Context, tx *sql.Tx, statements []string) error {
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

type contextQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func hasColumn(ctx context.Context, queryer contextQueryer, table, column string) (bool, error) {
	rows, err := queryer.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) verifyFTS5() error {
	_, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS dogear_fts5_check USING fts5(value); DROP TABLE dogear_fts5_check;`)
	return err
}
