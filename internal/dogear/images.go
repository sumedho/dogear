package dogear

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

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
