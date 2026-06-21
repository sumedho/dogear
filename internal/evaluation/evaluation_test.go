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

func TestGuideAnswerMetrics(t *testing.T) {
	fixture := Fixture{Cases: []Case{{ID: "guide", Query: "setup", Relevant: []Relevant{{HeadingPath: "Setup"}}, ExpectedSections: []string{"Prerequisites", "Verification", "Troubleshooting"}, RequireOrderedSteps: true, RequireConflictNotice: true}}}
	retrieve := func(context.Context, string, Case, int) (dogear.RetrievalResult, error) { return dogear.RetrievalResult{Blocks: []dogear.ContextBlock{{Source: dogear.SourceRef{HeadingPath: "Setup"}}}}, nil }
	answer := func(context.Context, string, Case) (string, error) { return "## Prerequisites\nReady.\n\n1. Configure it [1].\n\n## Verification\nTest it.\n\n## Troubleshooting\nThe manuals conflict on this setting.", nil }
	report := Run(context.Background(), fixture, "guide", []int{1}, retrieve, answer)
	result := report.Results[0]
	if result.GuideSectionRecall != 1 || !result.OrderedSteps || !result.ConflictNotice || result.CitationValidity != 1 { t.Fatalf("guide metrics = %#v", result) }
}
