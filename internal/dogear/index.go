package dogear

import (
	"context"
)

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
