package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type guideRetriever struct{ calls []RetrieveOptions }

func (r *guideRetriever) Retrieve(_ context.Context, options RetrieveOptions) (RetrievalResult, error) {
	r.calls = append(r.calls, options)
	id := int64(len(r.calls))
	return RetrievalResult{Mode: "fts", Blocks: []ContextBlock{{Source: SourceRef{ChunkID: id, Label: "[1]", DocumentID: "manual", Title: "Manual", HeadingPath: options.Query, StartLine: 1, EndLine: 2}, Text: strings.Repeat(options.Query+" ", 3)}}}, nil
}

func TestResolveResponseMode(t *testing.T) {
	if got := resolveResponseMode(ResponseModeAuto, "Walk me through the setup"); got != ResponseModeGuide {
		t.Fatalf("mode = %q", got)
	}
	if got := resolveResponseMode(ResponseModeAuto, "What is local control?"); got != ResponseModeAnswer {
		t.Fatalf("mode = %q", got)
	}
	if got := resolveResponseMode(ResponseModeAnswer, "How do I set it up?"); got != ResponseModeAnswer {
		t.Fatalf("explicit mode = %q", got)
	}
}

func TestParseGuidePlanValidatesAndBounds(t *testing.T) {
	sections := make([]string, 8)
	for index := range sections {
		sections[index] = fmt.Sprintf(`{"heading":"Section %d","queries":["query %d","query %d extra","ignored"]}`, index, index, index)
	}
	plan, err := parseGuidePlan(`prefix {"title":"Guide","sections":[`+strings.Join(sections, ",")+`]} suffix`, "question")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Sections) != guideMaxQueries/2 {
		t.Fatalf("sections = %d", len(plan.Sections))
	}
	queries := 0
	for _, section := range plan.Sections {
		queries += len(section.Queries)
		if len(section.Queries) > 2 {
			t.Fatal("section query limit exceeded")
		}
	}
	if queries != guideMaxQueries {
		t.Fatalf("queries = %d", queries)
	}
}

func TestRetrieveGuideGroupsAndRelabelsSources(t *testing.T) {
	retriever := &guideRetriever{}
	plan := GuideContext{Title: "Guide", Sections: []GuideSection{{Heading: "Setup", Queries: []string{"first", "second"}}, {Heading: "Verify", Queries: []string{"third"}}}}
	result, err := retrieveGuide(context.Background(), retriever, AskOptions{Question: "question", DocumentID: "manual"}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != 3 || result.Blocks[2].Source.Label != "[3]" {
		t.Fatalf("blocks = %#v", result.Blocks)
	}
	if got := result.Guide.Sections[0].SourceLabels; len(got) != 2 || got[0] != "[1]" || got[1] != "[2]" {
		t.Fatalf("labels = %#v", got)
	}
	for _, call := range retriever.calls {
		if call.DocumentID != "manual" || call.Limit != guideResultsPerQuery {
			t.Fatalf("call = %#v", call)
		}
	}
	prompt := PromptGuideContext(result)
	for _, want := range []string{"Guide title: Guide", "Setup: evidence [1], [2]", "[3]"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	messages := BuildGuideMessages(result, nil)
	if !strings.Contains(messages[0].Content, "conflicting instructions") || !strings.Contains(messages[0].Content, "numbered steps") {
		t.Fatalf("guide system prompt = %q", messages[0].Content)
	}
}

func TestFallbackGuidePlanCoversWorkflow(t *testing.T) {
	plan := fallbackGuidePlan("configure MIDI")
	joined := ""
	for _, section := range plan.Sections {
		joined += " " + section.Heading
	}
	for _, want := range []string{"Prerequisites", "Setup", "Verification", "Troubleshooting"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
