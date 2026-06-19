package dogearadapter

import (
	"database/sql"
	"testing"

	"github.com/sumedho/dogear/internal/dogear"
)

func TestRetrievalResult(t *testing.T) {
	result := retrievalResult(dogear.RetrievalResult{
		Query: "question",
		Blocks: []dogear.ContextBlock{
			{
				Source: dogear.SourceRef{
					Label: "[1]", DocumentID: "doc", Title: "Manual", Brand: "Brand", Model: "Model",
					HeadingPath: "Setup", PageNumber: sql.NullInt64{Int64: 4, Valid: true},
					StartLine: 3, EndLine: 8, Score: -1.25,
				},
				Text: "first",
			},
			{
				Source: dogear.SourceRef{Label: "[2]", Title: "Manual"},
				Text:   "second",
			},
		},
	})

	if result.Query != "question" || len(result.Blocks) != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	first := result.Blocks[0]
	if first.Source.PageNumber == nil || *first.Source.PageNumber != 4 || first.Source.Brand != "Brand" || first.Source.Score != -1.25 {
		t.Fatalf("unexpected first block: %#v", first)
	}
	if result.Blocks[1].Source.PageNumber != nil {
		t.Fatalf("second page number = %v, want nil", result.Blocks[1].Source.PageNumber)
	}
}
