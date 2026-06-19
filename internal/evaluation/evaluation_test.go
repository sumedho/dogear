package evaluation

import (
	"context"
	"database/sql"
	"testing"

	"github.com/sumedho/dogear/internal/dogear"
)

func TestRunMetrics(t *testing.T) {
	fixture := Fixture{Cases: []Case{{ID: "one", Query: "sync", Relevant: []Relevant{{HeadingPath: "Sync"}}}}}
	retrieve := func(context.Context, string, Case, int) (dogear.RetrievalResult, error) {
		return dogear.RetrievalResult{Blocks: []dogear.ContextBlock{{Source: dogear.SourceRef{HeadingPath: "MIDI > Other"}}, {Source: dogear.SourceRef{HeadingPath: "MIDI > Sync", PageNumber: sql.NullInt64{Int64: 2, Valid: true}}}}}, nil
	}
	report := Run(context.Background(), fixture, "fts", []int{1, 3, 5}, retrieve, nil)
	if report.MRR != .5 || report.RecallAt[1] != 0 || report.RecallAt[3] != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
}
