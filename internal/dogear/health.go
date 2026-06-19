package dogear

import (
	"context"
	"database/sql"
)

func (s *Store) DocumentHealth(ctx context.Context, documentID string, embedding EmbeddingIndexStatus) (DocumentHealth, error) {
	info, err := s.DocumentInfo(ctx, documentID)
	if err != nil {
		return DocumentHealth{}, err
	}
	health := DocumentHealth{
		DocumentID: documentID,
		ChunkCount: info.ChunkCount,
		Warnings:   info.ImportWarnings,
		FTS:        IndexCoverage{Total: info.ChunkCount, Indexed: info.IndexedChunks, Complete: info.ChunkCount == info.IndexedChunks},
		Vectors: VectorCoverage{
			Configured: embedding.Configured,
			Stale:      embedding.Stale,
			Model:      embedding.Model,
			UpdatedAt:  embedding.UpdatedAt,
		},
	}
	if health.Warnings == nil {
		health.Warnings = []DocumentImportWarning{}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM document_images WHERE document_id = ?`, documentID).Scan(&health.ImageCount); err != nil {
		return DocumentHealth{}, err
	}
	chunks, err := s.EmbeddingChunks(ctx)
	if err != nil {
		return DocumentHealth{}, err
	}
	for _, chunk := range chunks {
		if chunk.DocumentID == documentID {
			health.Vectors.Total++
		}
	}
	var vectorTable int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'chunk_embeddings')`).Scan(&vectorTable); err != nil {
		return DocumentHealth{}, err
	}
	if vectorTable != 0 {
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunk_embeddings e JOIN chunks c ON c.id = e.rowid WHERE c.document_id = ?`, documentID).Scan(&health.Vectors.Indexed)
		if err != nil && err != sql.ErrNoRows {
			return DocumentHealth{}, err
		}
	}
	health.Vectors.Complete = health.Vectors.Configured && !health.Vectors.Stale && health.Vectors.Indexed == health.Vectors.Total
	return health, nil
}
