package dogear

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

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
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.document_id, d.title, c.heading_path,
		c.text || CASE WHEN EXISTS(SELECT 1 FROM document_images di WHERE di.chunk_id = c.id)
			THEN char(10) || char(10) || 'Images:' || char(10) ||
				(SELECT group_concat(di.alt_text, char(10)) FROM document_images di WHERE di.chunk_id = c.id)
			ELSE '' END,
		c.text_hash || ':' || COALESCE((SELECT group_concat(di.content_hash, ':') FROM document_images di WHERE di.chunk_id = c.id), '')
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
		if !isSearchableSection(chunk.Heading, chunk.Text) {
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
