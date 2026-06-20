package dogear

import (
	"context"
	"database/sql"
)

type contextDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) RebuildIndex(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	count, err := rebuildIndexWith(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func rebuildIndexWith(ctx context.Context, db contextDB) (int, error) {
	if _, err := db.ExecContext(ctx, `DELETE FROM chunks_fts`); err != nil {
		return 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT c.id, c.document_id, d.title, COALESCE(d.brand, ''), COALESCE(d.model, ''), COALESCE(c.heading_path, ''),
			c.text || CASE WHEN EXISTS(SELECT 1 FROM document_images di WHERE di.chunk_id = c.id)
				THEN char(10) || char(10) || 'Images:' || char(10) ||
					(SELECT group_concat(di.alt_text, char(10)) FROM document_images di WHERE di.chunk_id = c.id)
				ELSE '' END
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
		ORDER BY c.id`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id                              int64
		documentID, title, brand, model string
		heading, text                   string
	}
	var candidates []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.documentID, &item.title, &item.brand, &item.model, &item.heading, &item.text); err != nil {
			rows.Close()
			return 0, err
		}
		if isSearchableSection(item.heading, item.text) {
			candidates = append(candidates, item)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, item := range candidates {
		if _, err := db.ExecContext(ctx, `INSERT INTO chunks_fts(chunk_id, document_id, title, brand, model, heading_path, text) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			item.id, item.documentID, item.title, item.brand, item.model, item.heading, item.text); err != nil {
			return 0, err
		}
	}
	return len(candidates), nil
}
