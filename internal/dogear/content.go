package dogear

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

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
