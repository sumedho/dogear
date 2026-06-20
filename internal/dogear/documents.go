package dogear

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/sumedho/dogear/internal/sqlutil"
)

func (s *Store) UpsertDocument(ctx context.Context, doc Document, chunks []Chunk, replace bool) error {
	return s.UpsertDocumentWithImages(ctx, doc, chunks, nil, replace)
}

func (s *Store) UpsertDocumentWithImages(ctx context.Context, doc Document, chunks []Chunk, images []DocumentImage, replace bool) error {
	tags, err := json.Marshal(doc.Tags)
	if err != nil {
		return err
	}
	warnings, err := json.Marshal(doc.ImportWarnings)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO documents(id, title, brand, model, version, source_path, source_hash, tags_json, import_warnings_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		doc.ID, doc.Title, doc.Brand, doc.Model, doc.Version, doc.SourcePath, doc.SourceHash, string(tags), string(warnings), timestamp, timestamp)
	if err != nil {
		return err
	}
	chunkIDs := make(map[int]int64, len(chunks))
	for _, chunk := range chunks {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO chunks(document_id, ordinal, heading_path, heading_level, page_number, start_line, end_line, text, text_hash)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			doc.ID, chunk.Ordinal, chunk.HeadingPath, chunk.HeadingLevel, sqlutil.Int64Value(chunk.PageNumber), chunk.StartLine, chunk.EndLine, chunk.Text, chunk.TextHash)
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
	rows, err := s.db.QueryContext(ctx, `WITH chunk_stats AS (
			SELECT document_id, COUNT(*) AS chunk_count, COUNT(DISTINCT page_number) AS page_count FROM chunks GROUP BY document_id
		), fts_stats AS (
			SELECT document_id, COUNT(*) AS indexed_chunks FROM chunks_fts GROUP BY document_id
		)
		SELECT d.id, d.title, d.brand, d.model, d.version, d.source_path, d.source_hash, d.tags_json, d.import_warnings_json,
			d.created_at, d.updated_at,
			COALESCE(c.chunk_count, 0), COALESCE(f.indexed_chunks, 0), COALESCE(c.page_count, 0)
		FROM documents d
		LEFT JOIN chunk_stats c ON c.document_id = d.id
		LEFT JOIN fts_stats f ON f.document_id = d.id
		ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDocumentInfos(rows)
}

func (s *Store) DocumentInfo(ctx context.Context, id string) (DocumentInfo, error) {
	rows, err := s.db.QueryContext(ctx, `WITH chunk_stats AS (
			SELECT document_id, COUNT(*) AS chunk_count, COUNT(DISTINCT page_number) AS page_count FROM chunks WHERE document_id = ? GROUP BY document_id
		), fts_stats AS (
			SELECT document_id, COUNT(*) AS indexed_chunks FROM chunks_fts WHERE document_id = ? GROUP BY document_id
		)
		SELECT d.id, d.title, d.brand, d.model, d.version, d.source_path, d.source_hash, d.tags_json, d.import_warnings_json,
			d.created_at, d.updated_at,
			COALESCE(c.chunk_count, 0), COALESCE(f.indexed_chunks, 0), COALESCE(c.page_count, 0)
		FROM documents d
		LEFT JOIN chunk_stats c ON c.document_id = d.id
		LEFT JOIN fts_stats f ON f.document_id = d.id
		WHERE d.id = ?
		`, id, id, id)
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

func scanDocumentInfos(rows *sql.Rows) ([]DocumentInfo, error) {
	var infos []DocumentInfo
	for rows.Next() {
		var info DocumentInfo
		var tagsJSON string
		var warningsJSON string
		if err := rows.Scan(&info.ID, &info.Title, &info.Brand, &info.Model, &info.Version, &info.SourcePath, &info.SourceHash, &tagsJSON, &warningsJSON,
			&info.CreatedAt, &info.UpdatedAt, &info.ChunkCount, &info.IndexedChunks, &info.PageCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &info.Tags); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(warningsJSON), &info.ImportWarnings); err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
}
