package dogear

import (
	"context"
)

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
