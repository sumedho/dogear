package dogear

const schemaVersion = 3

func (s *Store) Init() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			brand TEXT,
			model TEXT,
			version TEXT,
			source_path TEXT NOT NULL,
			source_hash TEXT NOT NULL,
			tags_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id INTEGER PRIMARY KEY,
			document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
			ordinal INTEGER NOT NULL,
			heading_path TEXT,
			heading_level INTEGER,
			page_number INTEGER,
			start_line INTEGER,
			end_line INTEGER,
			text TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			UNIQUE(document_id, ordinal)
		);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			chunk_id UNINDEXED,
			document_id UNINDEXED,
			title,
			brand,
			model,
			heading_path,
			text,
			tokenize='unicode61'
		);`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_document_id ON chunks(document_id);`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_page ON chunks(document_id, page_number);`,
		`CREATE INDEX IF NOT EXISTS idx_documents_source_hash ON documents(source_hash);`,
		`CREATE TABLE IF NOT EXISTS document_images (
			id INTEGER PRIMARY KEY,
			document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
			chunk_id INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
			ordinal INTEGER NOT NULL,
			alt_text TEXT NOT NULL,
			media_type TEXT NOT NULL,
			data BLOB NOT NULL,
			content_hash TEXT NOT NULL,
			UNIQUE(document_id, ordinal)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_document_images_chunk_id ON document_images(chunk_id);`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?);`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(2, ?);`,
		`CREATE TABLE IF NOT EXISTS embedding_index_state (
			id INTEGER PRIMARY KEY CHECK(id = 1), model TEXT NOT NULL, dimensions INTEGER NOT NULL,
			config_hash TEXT NOT NULL, complete INTEGER NOT NULL DEFAULT 0,
			indexed_count INTEGER NOT NULL DEFAULT 0, total_count INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		);`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(3, ?);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt, now()); err != nil {
			return err
		}
	}
	return s.verifyFTS5()
}

func (s *Store) verifyFTS5() error {
	_, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS dogear_fts5_check USING fts5(value); DROP TABLE dogear_fts5_check;`)
	return err
}
