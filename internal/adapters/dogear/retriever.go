package dogearadapter

import (
	"context"
	"database/sql"

	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
)

type Retriever struct {
	store *dogear.Store
}

func NewRetriever(store *dogear.Store) Retriever {
	return Retriever{store: store}
}

func (r Retriever) Retrieve(ctx context.Context, opts app.RetrieveOptions) (app.RetrievalResult, error) {
	result, err := r.store.Retrieve(ctx, dogear.RetrieveOptions{
		Query:      opts.Query,
		DocumentID: opts.DocumentID,
		Limit:      opts.Limit,
	})
	if err != nil {
		return app.RetrievalResult{}, err
	}
	return retrievalResult(result), nil
}

func retrievalResult(result dogear.RetrievalResult) app.RetrievalResult {
	out := app.RetrievalResult{
		Query:  result.Query,
		Blocks: make([]app.ContextBlock, 0, len(result.Blocks)),
	}
	for _, block := range result.Blocks {
		images := make([]app.ImageRef, 0, len(block.Images))
		for _, image := range block.Images {
			images = append(images, app.ImageRef{ID: image.ID, Alt: image.Alt, MediaType: image.MediaType})
		}
		out.Blocks = append(out.Blocks, app.ContextBlock{
			Source: app.SourceRef{
				Label:       block.Source.Label,
				DocumentID:  block.Source.DocumentID,
				Title:       block.Source.Title,
				Brand:       block.Source.Brand,
				Model:       block.Source.Model,
				HeadingPath: block.Source.HeadingPath,
				PageNumber:  nullInt64Ptr(block.Source.PageNumber),
				StartLine:   block.Source.StartLine,
				EndLine:     block.Source.EndLine,
				Score:       block.Source.Score,
			},
			Text:   block.Text,
			Images: images,
		})
	}
	return out
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
