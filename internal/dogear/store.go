package dogear

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

const schemaVersion = 3

type Store struct {
	db   *sql.DB
	path string
}

type Document struct {
	ID         string
	Title      string
	Brand      string
	Model      string
	Version    string
	SourcePath string
	SourceHash string
	Tags       []string
}

type DocumentInfo struct {
	Document
	CreatedAt     string
	UpdatedAt     string
	ChunkCount    int
	IndexedChunks int
	PageCount     int
}

type Chunk struct {
	ID           int64
	DocumentID   string
	Ordinal      int
	HeadingPath  string
	HeadingLevel int
	PageNumber   sql.NullInt64
	StartLine    int
	EndLine      int
	Text         string
	TextHash     string
}

type DocumentChunk struct {
	Chunk
	Images []ImageRef
}

type SearchOptions struct {
	Query      string
	DocumentID string
	Limit      int
	Debug      bool
}

type SearchResult struct {
	ChunkID     int64
	DocumentID  string
	Title       string
	HeadingPath string
	PageNumber  sql.NullInt64
	StartLine   int
	EndLine     int
	Snippet     string
	Score       float64
	Debug       RankDebug
}

type RetrieveOptions struct {
	Query      string
	DocumentID string
	Limit      int
	Debug      bool
}

type RetrievedChunk struct {
	ChunkID     int64
	DocumentID  string
	Title       string
	Brand       string
	Model       string
	HeadingPath string
	PageNumber  sql.NullInt64
	StartLine   int
	EndLine     int
	Text        string
	Score       float64
	Debug       RankDebug
}

type SourceRef struct {
	ChunkID     int64
	Label       string
	DocumentID  string
	Title       string
	Brand       string
	Model       string
	HeadingPath string
	PageNumber  sql.NullInt64
	StartLine   int
	EndLine     int
	Score       float64
	Debug       RankDebug
}

type ContextBlock struct {
	Source SourceRef
	Text   string
	Images []ImageRef
}

type ImageRef struct {
	ID        int64
	Alt       string
	MediaType string
}

type StoredImage struct {
	ImageRef
	Data        []byte
	ContentHash string
}

type RetrievalResult struct {
	Query  string
	Blocks []ContextBlock
}

type RankDebug struct {
	RawScore       float64
	RerankScore    float64
	Quality        string
	Reasons        []string
	Mode           string
	FTSRank        int
	VectorRank     int
	VectorDistance float64
	FusedScore     float64
	FallbackReason string
}

type ShowOptions struct {
	DocumentID string
	Page       int
	Section    string
}

type DoctorReport struct {
	SchemaVersion int
	FTS5          bool
	Documents     int
	Chunks        int
	IndexedChunks int
	OrphanChunks  int
}

type EmbeddingIndexStatus struct {
	Configured bool   `json:"configured"`
	Complete   bool   `json:"complete"`
	Stale      bool   `json:"stale"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	Indexed    int    `json:"indexed"`
	Total      int    `json:"total"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type EmbeddingChunk struct {
	ID         int64
	DocumentID string
	Title      string
	Heading    string
	Text       string
	TextHash   string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: path}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

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

func (s *Store) UpsertDocument(ctx context.Context, doc Document, chunks []Chunk, replace bool) error {
	return s.UpsertDocumentWithImages(ctx, doc, chunks, nil, replace)
}

func (s *Store) UpsertDocumentWithImages(ctx context.Context, doc Document, chunks []Chunk, images []DocumentImage, replace bool) error {
	tags, err := json.Marshal(doc.Tags)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE id = ?)`, doc.ID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists && !replace {
		return fmt.Errorf("document %q already exists; pass --replace to replace it", doc.ID)
	}
	if exists {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE document_id = ?`, doc.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, doc.ID); err != nil {
			return err
		}
	}

	timestamp := now()
	_, err = tx.ExecContext(ctx, `INSERT INTO documents(id, title, brand, model, version, source_path, source_hash, tags_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		doc.ID, doc.Title, doc.Brand, doc.Model, doc.Version, doc.SourcePath, doc.SourceHash, string(tags), timestamp, timestamp)
	if err != nil {
		return err
	}
	chunkIDs := make(map[int]int64, len(chunks))
	for _, chunk := range chunks {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO chunks(document_id, ordinal, heading_path, heading_level, page_number, start_line, end_line, text, text_hash)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			doc.ID, chunk.Ordinal, chunk.HeadingPath, chunk.HeadingLevel, nullInt(chunk.PageNumber), chunk.StartLine, chunk.EndLine, chunk.Text, chunk.TextHash)
		if insertErr != nil {
			return insertErr
		}
		chunkID, insertErr := result.LastInsertId()
		if insertErr != nil {
			return insertErr
		}
		chunkIDs[chunk.Ordinal] = chunkID
	}
	for _, image := range images {
		chunkID, ok := chunkIDs[image.ChunkOrdinal]
		if !ok {
			return fmt.Errorf("image %d references missing chunk ordinal %d", image.Ordinal, image.ChunkOrdinal)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO document_images(document_id, chunk_id, ordinal, alt_text, media_type, data, content_hash)
			VALUES(?, ?, ?, ?, ?, ?, ?)`, doc.ID, chunkID, image.Ordinal, image.Alt, image.MediaType, image.Data, image.ContentHash); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE embedding_index_state SET complete = 0`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListDocuments(ctx context.Context) ([]DocumentInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.title, d.brand, d.model, d.version, d.source_path, d.source_hash, d.tags_json,
			d.created_at, d.updated_at,
			COUNT(DISTINCT c.id) AS chunk_count,
			COUNT(DISTINCT f.rowid) AS indexed_chunks,
			COUNT(DISTINCT c.page_number) AS page_count
		FROM documents d
		LEFT JOIN chunks c ON c.document_id = d.id
		LEFT JOIN chunks_fts f ON f.chunk_id = c.id
		GROUP BY d.id
		ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDocumentInfos(rows)
}

func (s *Store) DocumentInfo(ctx context.Context, id string) (DocumentInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.title, d.brand, d.model, d.version, d.source_path, d.source_hash, d.tags_json,
			d.created_at, d.updated_at,
			COUNT(DISTINCT c.id) AS chunk_count,
			COUNT(DISTINCT f.rowid) AS indexed_chunks,
			COUNT(DISTINCT c.page_number) AS page_count
		FROM documents d
		LEFT JOIN chunks c ON c.document_id = d.id
		LEFT JOIN chunks_fts f ON f.chunk_id = c.id
		WHERE d.id = ?
		GROUP BY d.id`, id)
	if err != nil {
		return DocumentInfo{}, err
	}
	defer rows.Close()
	infos, err := scanDocumentInfos(rows)
	if err != nil {
		return DocumentInfo{}, err
	}
	if len(infos) == 0 {
		return DocumentInfo{}, fmt.Errorf("document %q not found", id)
	}
	return infos[0], nil
}

func (s *Store) RemoveDocument(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE document_id = ?`, id)
	if err != nil {
		return err
	}
	_ = result
	result, err = tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("document %q not found", id)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE embedding_index_state SET complete = 0`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RebuildIndex(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts`); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO chunks_fts(chunk_id, document_id, title, brand, model, heading_path, text)
		SELECT c.id, c.document_id, d.title, d.brand, d.model, c.heading_path, c.text
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE lower(c.heading_path) NOT LIKE '%table of contents%'
		  AND lower(c.heading_path) NOT LIKE 'index%'
		  AND lower(c.heading_path) NOT LIKE '%credits%'
		  AND lower(c.heading_path) NOT LIKE '%contact information%'`)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}

func (s *Store) Retrieve(ctx context.Context, opts RetrieveOptions) (RetrievalResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	query := NormalizeFTSQuery(opts.Query)
	if query == "" {
		return RetrievalResult{}, errors.New("empty retrieval query")
	}

	result, err := s.retrieveWithQuery(ctx, opts, query, opts.Limit*5)
	if err != nil {
		return RetrievalResult{}, err
	}
	if len(result.Blocks) < opts.Limit && strings.Contains(query, " AND ") {
		fallback, err := s.retrieveWithQuery(ctx, opts, strings.ReplaceAll(query, " AND ", " OR "), opts.Limit*5)
		if err != nil {
			return RetrievalResult{}, err
		}
		result.Blocks = mergeBlocks(result.Blocks, fallback.Blocks)
	}
	chunks := make([]RetrievedChunk, 0, len(result.Blocks))
	for _, block := range result.Blocks {
		chunks = append(chunks, RetrievedChunk{
			ChunkID:     block.Source.ChunkID,
			DocumentID:  block.Source.DocumentID,
			Title:       block.Source.Title,
			Brand:       block.Source.Brand,
			Model:       block.Source.Model,
			HeadingPath: block.Source.HeadingPath,
			PageNumber:  block.Source.PageNumber,
			StartLine:   block.Source.StartLine,
			EndLine:     block.Source.EndLine,
			Text:        block.Text,
			Score:       block.Source.Score,
		})
	}
	reranked := rerankChunks(opts.Query, chunks, opts.Limit)
	out := RetrievalResult{Query: opts.Query, Blocks: make([]ContextBlock, 0, len(reranked))}
	for i, chunk := range reranked {
		chunk.Debug = rankCandidate(chunk, uniqueTerms(tokenize(NormalizeFTSQuery(opts.Query))))
		source := SourceRef{
			ChunkID:     chunk.ChunkID,
			Label:       fmt.Sprintf("[%d]", i+1),
			DocumentID:  chunk.DocumentID,
			Title:       chunk.Title,
			Brand:       chunk.Brand,
			Model:       chunk.Model,
			HeadingPath: chunk.HeadingPath,
			PageNumber:  chunk.PageNumber,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Score:       chunk.Score,
			Debug:       chunk.Debug,
		}
		images, err := s.imagesForChunk(ctx, chunk.ChunkID)
		if err != nil {
			return RetrievalResult{}, err
		}
		out.Blocks = append(out.Blocks, ContextBlock{Source: source, Text: chunk.Text, Images: images})
	}
	return out, nil
}

type hybridRanks struct {
	ftsRank    int
	vectorRank int
	distance   float64
	fused      float64
}

func (s *Store) RetrieveHybrid(ctx context.Context, opts RetrieveOptions, queryVector []float32) (RetrievalResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	query := NormalizeFTSQuery(opts.Query)
	if query == "" {
		return RetrievalResult{}, errors.New("empty retrieval query")
	}
	fetchLimit := opts.Limit * 5
	lexical, err := s.retrieveWithQuery(ctx, opts, query, fetchLimit)
	if err != nil {
		return RetrievalResult{}, err
	}
	if len(lexical.Blocks) < fetchLimit && strings.Contains(query, " AND ") {
		fallback, err := s.retrieveWithQuery(ctx, opts, strings.ReplaceAll(query, " AND ", " OR "), fetchLimit)
		if err != nil {
			return RetrievalResult{}, err
		}
		lexical.Blocks = mergeBlocks(lexical.Blocks, fallback.Blocks)
	}
	rawVector, err := json.Marshal(queryVector)
	if err != nil {
		return RetrievalResult{}, err
	}
	querySQL := `SELECT rowid, distance FROM chunk_embeddings WHERE embedding MATCH ? AND k = ?`
	args := []any{string(rawVector), fetchLimit}
	if opts.DocumentID != "" {
		querySQL += ` AND document_id = ?`
		args = append(args, opts.DocumentID)
	}
	querySQL += ` ORDER BY distance`
	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return RetrievalResult{}, err
	}
	type vectorHit struct {
		id       int64
		distance float64
	}
	var vectorHits []vectorHit
	for rows.Next() {
		var hit vectorHit
		if err := rows.Scan(&hit.id, &hit.distance); err != nil {
			rows.Close()
			return RetrievalResult{}, err
		}
		vectorHits = append(vectorHits, hit)
	}
	if err := rows.Close(); err != nil {
		return RetrievalResult{}, err
	}

	chunks := map[int64]RetrievedChunk{}
	ranks := map[int64]*hybridRanks{}
	for i, block := range lexical.Blocks {
		id := block.Source.ChunkID
		chunks[id] = RetrievedChunk{ChunkID: id, DocumentID: block.Source.DocumentID, Title: block.Source.Title, Brand: block.Source.Brand, Model: block.Source.Model, HeadingPath: block.Source.HeadingPath, PageNumber: block.Source.PageNumber, StartLine: block.Source.StartLine, EndLine: block.Source.EndLine, Text: block.Text, Score: block.Source.Score}
		ranks[id] = &hybridRanks{ftsRank: i + 1}
	}
	for i, hit := range vectorHits {
		rank := ranks[hit.id]
		if rank == nil {
			rank = &hybridRanks{}
			ranks[hit.id] = rank
		}
		rank.vectorRank = i + 1
		rank.distance = hit.distance
		if _, ok := chunks[hit.id]; !ok {
			chunk, err := s.retrievedChunk(ctx, hit.id)
			if err != nil {
				return RetrievalResult{}, err
			}
			chunks[hit.id] = chunk
		}
	}
	const rrfK = 60.0
	candidates := make([]RetrievedChunk, 0, len(chunks))
	for id, chunk := range chunks {
		rank := ranks[id]
		if rank.ftsRank > 0 {
			rank.fused += 1 / (rrfK + float64(rank.ftsRank))
		}
		if rank.vectorRank > 0 {
			rank.fused += 1 / (rrfK + float64(rank.vectorRank))
		}
		chunk.Score = -rank.fused * 100
		candidates = append(candidates, chunk)
	}
	ranked := rerankChunks(opts.Query, candidates, opts.Limit)
	out := RetrievalResult{Query: opts.Query, Blocks: make([]ContextBlock, 0, len(ranked))}
	for i, chunk := range ranked {
		rank := ranks[chunk.ChunkID]
		debug := chunk.Debug
		debug.Mode = "hybrid"
		debug.FTSRank = rank.ftsRank
		debug.VectorRank = rank.vectorRank
		debug.VectorDistance = rank.distance
		debug.FusedScore = rank.fused
		source := SourceRef{ChunkID: chunk.ChunkID, Label: fmt.Sprintf("[%d]", i+1), DocumentID: chunk.DocumentID, Title: chunk.Title, Brand: chunk.Brand, Model: chunk.Model, HeadingPath: chunk.HeadingPath, PageNumber: chunk.PageNumber, StartLine: chunk.StartLine, EndLine: chunk.EndLine, Score: chunk.Score, Debug: debug}
		images, err := s.imagesForChunk(ctx, chunk.ChunkID)
		if err != nil {
			return RetrievalResult{}, err
		}
		out.Blocks = append(out.Blocks, ContextBlock{Source: source, Text: chunk.Text, Images: images})
	}
	return out, nil
}

func (s *Store) retrievedChunk(ctx context.Context, id int64) (RetrievedChunk, error) {
	var chunk RetrievedChunk
	err := s.db.QueryRowContext(ctx, `SELECT c.id, c.document_id, d.title, d.brand, d.model, c.heading_path, c.page_number, c.start_line, c.end_line, c.text
		FROM chunks c JOIN documents d ON d.id = c.document_id WHERE c.id = ?`, id).
		Scan(&chunk.ChunkID, &chunk.DocumentID, &chunk.Title, &chunk.Brand, &chunk.Model, &chunk.HeadingPath, &chunk.PageNumber, &chunk.StartLine, &chunk.EndLine, &chunk.Text)
	return chunk, err
}

func (s *Store) Image(ctx context.Context, id int64) (StoredImage, error) {
	var image StoredImage
	image.ID = id
	err := s.db.QueryRowContext(ctx, `SELECT alt_text, media_type, data, content_hash FROM document_images WHERE id = ?`, id).
		Scan(&image.Alt, &image.MediaType, &image.Data, &image.ContentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredImage{}, fmt.Errorf("image %d not found", id)
	}
	return image, err
}

func (s *Store) imagesForChunk(ctx context.Context, chunkID int64) ([]ImageRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, alt_text, media_type FROM document_images WHERE chunk_id = ? ORDER BY ordinal`, chunkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var images []ImageRef
	for rows.Next() {
		var image ImageRef
		if err := rows.Scan(&image.ID, &image.Alt, &image.MediaType); err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	return images, rows.Err()
}

func (s *Store) retrieveWithQuery(ctx context.Context, opts RetrieveOptions, query string, fetchLimit int) (RetrievalResult, error) {
	if fetchLimit <= 0 {
		fetchLimit = opts.Limit
	}
	args := []any{query}
	where := `chunks_fts MATCH ?`
	if opts.DocumentID != "" {
		where += ` AND f.document_id = ?`
		args = append(args, opts.DocumentID)
	}
	args = append(args, fetchLimit)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT c.id, f.document_id, d.title, d.brand, d.model, c.heading_path, c.page_number,
			c.start_line, c.end_line, c.text, bm25(chunks_fts) AS score
		FROM chunks_fts f
		JOIN chunks c ON c.id = f.chunk_id
		JOIN documents d ON d.id = f.document_id
		WHERE %s
		ORDER BY score
		LIMIT ?`, where), args...)
	if err != nil {
		return RetrievalResult{}, err
	}
	defer rows.Close()

	result := RetrievalResult{Query: opts.Query}
	for rows.Next() {
		var chunk RetrievedChunk
		if err := rows.Scan(&chunk.ChunkID, &chunk.DocumentID, &chunk.Title, &chunk.Brand, &chunk.Model, &chunk.HeadingPath, &chunk.PageNumber, &chunk.StartLine, &chunk.EndLine, &chunk.Text, &chunk.Score); err != nil {
			return RetrievalResult{}, err
		}
		if qualityClass(chunk.HeadingPath, chunk.Text) == QualityTOC || qualityClass(chunk.HeadingPath, chunk.Text) == QualityIndex || qualityClass(chunk.HeadingPath, chunk.Text) == QualityReferenceOnly {
			continue
		}
		source := SourceRef{
			ChunkID:     chunk.ChunkID,
			Label:       fmt.Sprintf("[%d]", len(result.Blocks)+1),
			DocumentID:  chunk.DocumentID,
			Title:       chunk.Title,
			Brand:       chunk.Brand,
			Model:       chunk.Model,
			HeadingPath: chunk.HeadingPath,
			PageNumber:  chunk.PageNumber,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Score:       chunk.Score,
		}
		result.Blocks = append(result.Blocks, ContextBlock{Source: source, Text: chunk.Text})
	}
	return result, rows.Err()
}

func (s *Store) Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	query := NormalizeFTSQuery(opts.Query)
	if query == "" {
		return nil, errors.New("empty search query")
	}

	fetchLimit := opts.Limit * 5
	candidates, err := s.searchWithQuery(ctx, opts, query, fetchLimit)
	if err != nil {
		return nil, err
	}
	if len(candidates) < opts.Limit && strings.Contains(query, " AND ") {
		fallback, err := s.searchWithQuery(ctx, opts, strings.ReplaceAll(query, " AND ", " OR "), fetchLimit)
		if err != nil {
			return nil, err
		}
		candidates = mergeSearchResults(candidates, fallback)
	}
	chunks := make([]RetrievedChunk, 0, len(candidates))
	snippets := map[int64]string{}
	for _, candidate := range candidates {
		chunks = append(chunks, RetrievedChunk{
			ChunkID:     candidate.ChunkID,
			DocumentID:  candidate.DocumentID,
			Title:       candidate.Title,
			HeadingPath: candidate.HeadingPath,
			PageNumber:  candidate.PageNumber,
			StartLine:   candidate.StartLine,
			EndLine:     candidate.EndLine,
			Text:        candidate.Snippet,
			Score:       candidate.Score,
		})
		snippets[candidate.ChunkID] = candidate.Snippet
	}
	reranked := rerankChunks(opts.Query, chunks, opts.Limit)
	out := make([]SearchResult, 0, len(reranked))
	for _, chunk := range reranked {
		out = append(out, SearchResult{
			ChunkID:     chunk.ChunkID,
			DocumentID:  chunk.DocumentID,
			Title:       chunk.Title,
			HeadingPath: chunk.HeadingPath,
			PageNumber:  chunk.PageNumber,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Snippet:     snippets[chunk.ChunkID],
			Score:       chunk.Score,
			Debug:       chunk.Debug,
		})
	}
	return out, nil
}

func (s *Store) SearchHybrid(ctx context.Context, opts SearchOptions, queryVector []float32) ([]SearchResult, error) {
	retrieval, err := s.RetrieveHybrid(ctx, RetrieveOptions{Query: opts.Query, DocumentID: opts.DocumentID, Limit: opts.Limit, Debug: opts.Debug}, queryVector)
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(retrieval.Blocks))
	for _, block := range retrieval.Blocks {
		results = append(results, SearchResult{ChunkID: block.Source.ChunkID, DocumentID: block.Source.DocumentID, Title: block.Source.Title,
			HeadingPath: block.Source.HeadingPath, PageNumber: block.Source.PageNumber, StartLine: block.Source.StartLine, EndLine: block.Source.EndLine,
			Snippet: block.Text, Score: block.Source.Score, Debug: block.Source.Debug})
	}
	return results, nil
}

func (s *Store) searchWithQuery(ctx context.Context, opts SearchOptions, query string, fetchLimit int) ([]SearchResult, error) {
	args := []any{query}
	where := `chunks_fts MATCH ?`
	if opts.DocumentID != "" {
		where += ` AND f.document_id = ?`
		args = append(args, opts.DocumentID)
	}
	args = append(args, fetchLimit)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT c.id, f.document_id, d.title, c.heading_path, c.page_number,
			c.start_line, c.end_line,
			snippet(chunks_fts, 6, '[', ']', ' ... ', 20) AS snippet,
			bm25(chunks_fts) AS score
		FROM chunks_fts f
		JOIN chunks c ON c.id = f.chunk_id
		JOIN documents d ON d.id = f.document_id
		WHERE %s
		ORDER BY score
		LIMIT ?`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(&result.ChunkID, &result.DocumentID, &result.Title, &result.HeadingPath, &result.PageNumber, &result.StartLine, &result.EndLine, &result.Snippet, &result.Score); err != nil {
			return nil, err
		}
		if qualityClass(result.HeadingPath, result.Snippet) == QualityTOC || qualityClass(result.HeadingPath, result.Snippet) == QualityIndex || qualityClass(result.HeadingPath, result.Snippet) == QualityReferenceOnly {
			continue
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s *Store) Show(ctx context.Context, opts ShowOptions) ([]Chunk, error) {
	args := []any{opts.DocumentID}
	where := `document_id = ?`
	if opts.Page > 0 {
		where += ` AND page_number = ?`
		args = append(args, opts.Page)
	}
	if opts.Section != "" {
		where += ` AND lower(heading_path) LIKE ?`
		args = append(args, "%"+strings.ToLower(opts.Section)+"%")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, document_id, ordinal, heading_path, heading_level, page_number, start_line, end_line, text, text_hash
		FROM chunks
		WHERE `+where+`
		ORDER BY ordinal`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []Chunk
	for rows.Next() {
		var chunk Chunk
		if err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Ordinal, &chunk.HeadingPath, &chunk.HeadingLevel, &chunk.PageNumber, &chunk.StartLine, &chunk.EndLine, &chunk.Text, &chunk.TextHash); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func (s *Store) DocumentChunks(ctx context.Context, documentID string, afterOrdinal, limit int) ([]DocumentChunk, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, document_id, ordinal, heading_path, heading_level, page_number, start_line, end_line, text, text_hash
		FROM chunks WHERE document_id = ? AND ordinal > ? ORDER BY ordinal LIMIT ?`, documentID, afterOrdinal, limit)
	if err != nil {
		return nil, err
	}
	var chunks []DocumentChunk
	for rows.Next() {
		var item DocumentChunk
		if err := rows.Scan(&item.ID, &item.DocumentID, &item.Ordinal, &item.HeadingPath, &item.HeadingLevel, &item.PageNumber, &item.StartLine, &item.EndLine, &item.Text, &item.TextHash); err != nil {
			return nil, err
		}
		chunks = append(chunks, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range chunks {
		images, err := s.imagesForChunk(ctx, chunks[i].ID)
		if err != nil {
			return nil, err
		}
		chunks[i].Images = images
	}
	return chunks, nil
}

func (s *Store) DocumentChunk(ctx context.Context, documentID string, chunkID int64) (DocumentChunk, error) {
	var item DocumentChunk
	err := s.db.QueryRowContext(ctx, `SELECT id, document_id, ordinal, heading_path, heading_level, page_number, start_line, end_line, text, text_hash
		FROM chunks WHERE document_id = ? AND id = ?`, documentID, chunkID).
		Scan(&item.ID, &item.DocumentID, &item.Ordinal, &item.HeadingPath, &item.HeadingLevel, &item.PageNumber, &item.StartLine, &item.EndLine, &item.Text, &item.TextHash)
	if errors.Is(err, sql.ErrNoRows) {
		return DocumentChunk{}, fmt.Errorf("chunk %d not found", chunkID)
	}
	if err != nil {
		return DocumentChunk{}, err
	}
	item.Images, err = s.imagesForChunk(ctx, item.ID)
	return item, err
}

func (s *Store) Doctor(ctx context.Context) (DoctorReport, error) {
	var report DoctorReport
	report.FTS5 = s.verifyFTS5() == nil
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&report.SchemaVersion)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents`).Scan(&report.Documents)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&report.Chunks)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks_fts`).Scan(&report.IndexedChunks)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks c LEFT JOIN documents d ON d.id = c.document_id WHERE d.id IS NULL`).Scan(&report.OrphanChunks)
	return report, nil
}

func (s *Store) verifyFTS5() error {
	_, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS dogear_fts5_check USING fts5(value); DROP TABLE dogear_fts5_check;`)
	return err
}

func (s *Store) EmbeddingStatus(ctx context.Context, model string, dimensions int, configHash string) (EmbeddingIndexStatus, error) {
	status := EmbeddingIndexStatus{Configured: model != "", Model: model, Dimensions: dimensions}
	chunks, err := s.EmbeddingChunks(ctx)
	if err != nil {
		return status, err
	}
	currentTotal := len(chunks)
	status.Total = currentTotal
	var complete int
	var storedHash string
	err = s.db.QueryRowContext(ctx, `SELECT model, dimensions, config_hash, complete, indexed_count, total_count, updated_at FROM embedding_index_state WHERE id = 1`).
		Scan(&status.Model, &status.Dimensions, &storedHash, &complete, &status.Indexed, &status.Total, &status.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		status.Model = model
		status.Dimensions = dimensions
		status.Stale = model != ""
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Complete = complete != 0 && status.Model == model && status.Dimensions == dimensions && storedHash == configHash && status.Indexed == currentTotal && status.Total == currentTotal
	status.Total = currentTotal
	status.Stale = model != "" && !status.Complete
	return status, nil
}

func (s *Store) EmbeddingChunks(ctx context.Context) ([]EmbeddingChunk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.document_id, d.title, c.heading_path, c.text, c.text_hash
		FROM chunks c JOIN documents d ON d.id = c.document_id ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chunks []EmbeddingChunk
	for rows.Next() {
		var chunk EmbeddingChunk
		if err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Title, &chunk.Heading, &chunk.Text, &chunk.TextHash); err != nil {
			return nil, err
		}
		if qualityClass(chunk.Heading, chunk.Text) == QualityTOC || qualityClass(chunk.Heading, chunk.Text) == QualityIndex || qualityClass(chunk.Heading, chunk.Text) == QualityReferenceOnly {
			continue
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func (s *Store) BuildEmbeddingIndex(ctx context.Context, model string, dimensions, batchSize int, configHash string, force bool, embed func(context.Context, []string) ([][]float32, error), progress func(indexed, total int)) (EmbeddingIndexStatus, error) {
	if model == "" {
		return EmbeddingIndexStatus{}, errors.New("embedding model is not configured")
	}
	if dimensions < 32 || dimensions > 4096 {
		return EmbeddingIndexStatus{}, errors.New("embedding dimensions must be between 32 and 4096")
	}
	status, err := s.EmbeddingStatus(ctx, model, dimensions, configHash)
	if err != nil {
		return status, err
	}
	if status.Complete && !force {
		return status, nil
	}
	chunks, err := s.EmbeddingChunks(ctx)
	if err != nil {
		return status, err
	}
	if batchSize <= 0 {
		batchSize = 16
	}
	vectors := make([][]float32, 0, len(chunks))
	for start := 0; start < len(chunks); start += batchSize {
		end := min(start+batchSize, len(chunks))
		input := make([]string, 0, end-start)
		for _, chunk := range chunks[start:end] {
			input = append(input, "Title: "+chunk.Title+"\nSection: "+chunk.Heading+"\n\n"+chunk.Text)
		}
		batch, err := embed(ctx, input)
		if err != nil {
			return status, err
		}
		if len(batch) != len(input) {
			return status, fmt.Errorf("embedding provider returned %d vectors for %d chunks", len(batch), len(input))
		}
		vectors = append(vectors, batch...)
		if progress != nil {
			progress(end, len(chunks))
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return status, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS chunk_embeddings`); err != nil {
		return status, err
	}
	create := fmt.Sprintf(`CREATE VIRTUAL TABLE chunk_embeddings USING vec0(embedding float[%d], document_id text partition key)`, dimensions)
	if _, err := tx.ExecContext(ctx, create); err != nil {
		return status, err
	}
	for i, chunk := range chunks {
		raw, err := json.Marshal(vectors[i])
		if err != nil {
			return status, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunk_embeddings(rowid, embedding, document_id) VALUES(?, ?, ?)`, chunk.ID, string(raw), chunk.DocumentID); err != nil {
			return status, err
		}
	}
	timestamp := now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO embedding_index_state(id, model, dimensions, config_hash, complete, indexed_count, total_count, updated_at)
		VALUES(1, ?, ?, ?, 1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET model=excluded.model, dimensions=excluded.dimensions, config_hash=excluded.config_hash,
		complete=1, indexed_count=excluded.indexed_count, total_count=excluded.total_count, updated_at=excluded.updated_at`,
		model, dimensions, configHash, len(chunks), len(chunks), timestamp); err != nil {
		return status, err
	}
	if err := tx.Commit(); err != nil {
		return status, err
	}
	return EmbeddingIndexStatus{Configured: true, Complete: true, Model: model, Dimensions: dimensions, Indexed: len(chunks), Total: len(chunks), UpdatedAt: timestamp}, nil
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func nullInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func scanDocumentInfos(rows *sql.Rows) ([]DocumentInfo, error) {
	var infos []DocumentInfo
	for rows.Next() {
		var info DocumentInfo
		var tagsJSON string
		if err := rows.Scan(&info.ID, &info.Title, &info.Brand, &info.Model, &info.Version, &info.SourcePath, &info.SourceHash, &tagsJSON,
			&info.CreatedAt, &info.UpdatedAt, &info.ChunkCount, &info.IndexedChunks, &info.PageCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &info.Tags); err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

func mergeBlocks(primary, secondary []ContextBlock) []ContextBlock {
	seen := map[int64]bool{}
	out := make([]ContextBlock, 0, len(primary)+len(secondary))
	for _, block := range primary {
		if seen[block.Source.ChunkID] {
			continue
		}
		seen[block.Source.ChunkID] = true
		out = append(out, block)
	}
	for _, block := range secondary {
		if seen[block.Source.ChunkID] {
			continue
		}
		seen[block.Source.ChunkID] = true
		out = append(out, block)
	}
	return out
}

func mergeSearchResults(primary, secondary []SearchResult) []SearchResult {
	seen := map[int64]bool{}
	out := make([]SearchResult, 0, len(primary)+len(secondary))
	for _, result := range primary {
		if seen[result.ChunkID] {
			continue
		}
		seen[result.ChunkID] = true
		out = append(out, result)
	}
	for _, result := range secondary {
		if seen[result.ChunkID] {
			continue
		}
		seen[result.ChunkID] = true
		out = append(out, result)
	}
	return out
}
