package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRetriever struct {
	options RetrieveOptions
	result  RetrievalResult
	err     error
}

func (f *fakeRetriever) Retrieve(_ context.Context, options RetrieveOptions) (RetrievalResult, error) {
	f.options = options
	return f.result, f.err
}

func TestAskUsesRetriever(t *testing.T) {
	t.Setenv("DOGEAR_BASE_URL", "")
	t.Setenv("DOGEAR_API_KEY", "")
	t.Setenv("DOGEAR_MODEL", "")
	t.Setenv("DOGEAR_TIMEOUT", "")
	page := int64(12)
	retriever := &fakeRetriever{result: RetrievalResult{
		Query: "local control",
		Blocks: []ContextBlock{{
			Source: SourceRef{
				Label: "[1]", DocumentID: "synth", Title: "Synth Manual",
				HeadingPath: "MIDI > Local Control", PageNumber: &page,
				StartLine: 10, EndLine: 14, Score: -2.5,
			},
			Text: "Set Local Control to Off.",
		}},
	}}

	result, err := Ask(context.Background(), retriever, AskOptions{
		Question:   "local control",
		DocumentID: "synth",
		DryRun:     true,
		ConfigPath: filepath.Join(t.TempDir(), "missing.toml"),
		Provider:   ProviderOverride{Model: "test-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if retriever.options != (RetrieveOptions{Query: "local control", DocumentID: "synth", Limit: 8}) {
		t.Fatalf("retrieval options = %#v", retriever.options)
	}
	if result.DryRun == nil || result.DryRun.Body.Model != "test-model" {
		t.Fatalf("unexpected dry run: %#v", result.DryRun)
	}
	if len(result.Sources) != 1 || result.Sources[0].PageNumber == nil || *result.Sources[0].PageNumber != page {
		t.Fatalf("unexpected sources: %#v", result.Sources)
	}
	if len(result.Retrieval.Blocks) != 1 || result.Retrieval.Blocks[0].Text != "Set Local Control to Off." {
		t.Fatalf("unexpected retrieval: %#v", result.Retrieval)
	}
}

func TestAskRetrievalFailures(t *testing.T) {
	wantErr := errors.New("retrieve failed")
	if _, err := Ask(context.Background(), &fakeRetriever{err: wantErr}, AskOptions{Question: "question"}); !errors.Is(err, wantErr) {
		t.Fatalf("Ask() error = %v, want %v", err, wantErr)
	}

	_, err := Ask(context.Background(), &fakeRetriever{result: RetrievalResult{Query: "question"}}, AskOptions{Question: "question"})
	if err == nil || !strings.Contains(err.Error(), "no context found") {
		t.Fatalf("Ask() error = %v, want no context error", err)
	}
}

func TestPromptContext(t *testing.T) {
	page := int64(7)
	retrieval := RetrievalResult{
		Query: "How?",
		Blocks: []ContextBlock{{
			Source: SourceRef{Label: "[1]", Title: "Manual", PageNumber: &page, HeadingPath: "Setup", StartLine: 2, EndLine: 4},
			Text:   "Use this setting.",
			Images: []ImageRef{{ID: 9, Alt: "Front panel", MediaType: "image/png"}},
		}},
	}

	prompt := PromptContext(retrieval)
	for _, want := range []string{"Question: How?", "[1] | Manual | p.7 | Setup | lines 2-4", "Use this setting.", `image 9: "Front panel" (image/png) from [1]`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q does not contain %q", prompt, want)
		}
	}
	messages := BuildAskMessages(retrieval)
	if len(messages) != 2 || messages[1].Content != prompt {
		t.Fatalf("unexpected messages: %#v", messages)
	}
	if !strings.Contains(messages[0].Content, "displayed below") || !strings.Contains(messages[0].Content, "cannot inspect image pixels") {
		t.Fatalf("system prompt is not image-aware: %q", messages[0].Content)
	}
}

func TestDisplayImagesForImageRequests(t *testing.T) {
	source := SourceRef{Label: "[1]", DocumentID: "synth", ChunkID: 12, Title: "Manual"}
	retrieval := RetrievalResult{Blocks: []ContextBlock{
		{Source: source, Images: []ImageRef{{ID: 4, Alt: "Front panel", MediaType: "image/png"}}},
		{Source: SourceRef{Label: "[2]"}, Images: []ImageRef{{ID: 4, Alt: "Duplicate", MediaType: "image/png"}, {ID: 5, Alt: "Rear panel", MediaType: "image/jpeg"}}},
	}}

	images := displayImages("Display the front panel", retrieval)
	if len(images) != 2 || images[0].ID != 4 || images[0].Source.ChunkID != 12 || images[1].ID != 5 {
		t.Fatalf("unexpected display images: %#v", images)
	}
	if images := displayImages("How do I change the MIDI channel?", retrieval); len(images) != 0 {
		t.Fatalf("ordinary question returned display images: %#v", images)
	}
	for _, question := range []string{"show a picture", "front panel layout", "is there a schematic?", "display the rear panel"} {
		if !wantsImages(question) {
			t.Fatalf("wantsImages(%q) = false", question)
		}
	}
	if wantsImages("show me how to configure MIDI") {
		t.Fatal("generic show request was classified as image intent")
	}
}

func TestBuildAskMessagesWithHistory(t *testing.T) {
	retrieval := RetrievalResult{Query: "follow up", Blocks: []ContextBlock{{Source: SourceRef{Label: "[1]", Title: "Manual", StartLine: 1, EndLine: 2}, Text: "Grounded text"}}}
	messages := BuildAskMessagesWithHistory(retrieval, []ConversationMessage{
		{Role: "user", Content: "First question"},
		{Role: "assistant", Content: "First answer"},
		{Role: "system", Content: "ignored"},
	})
	if len(messages) != 4 || messages[1].Role != "user" || messages[1].Content != "First question" || messages[2].Role != "assistant" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
	if messages[3].Role != "user" || !strings.Contains(messages[3].Content, "Question: follow up") {
		t.Fatalf("unexpected grounded message: %#v", messages[3])
	}
}
