package dogear

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	got := Slug("Yamaha DX7 Owner's Manual")
	want := "yamaha-dx7-owner-s-manual"
	if got != want {
		t.Fatalf("Slug() = %q, want %q", got, want)
	}
}

func TestNormalizeFTSQuery(t *testing.T) {
	got := NormalizeFTSQuery("local control!")
	want := "local AND control"
	if got != want {
		t.Fatalf("NormalizeFTSQuery() = %q, want %q", got, want)
	}

	got = NormalizeFTSQuery("How do I turn off local control?")
	want = "turn AND off AND local AND control"
	if got != want {
		t.Fatalf("NormalizeFTSQuery() = %q, want %q", got, want)
	}
}

func TestParseMarkdownFileInfersPageFromTOC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Yamaha-DX7.md")
	content := `## Yamaha DX7 User Manual

## TABLE OF CONTENTS

| 12.3 MIDI CONFIG . . . | . . 58 |

## 12.3 MIDI CONFIG

Set local control from this page.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, chunks, err := parseMarkdownFile(path, ImportMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.ID != "yamaha-dx7-user-manual" {
		t.Fatalf("doc.ID = %q", doc.ID)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	var found bool
	for _, chunk := range chunks {
		if chunk.HeadingPath == "12.3 MIDI CONFIG" {
			found = true
			if !chunk.PageNumber.Valid || chunk.PageNumber.Int64 != 58 {
				t.Fatalf("page = %v, want 58", chunk.PageNumber)
			}
		}
	}
	if !found {
		t.Fatalf("expected MIDI CONFIG chunk, got %#v", chunks)
	}
}

func TestParseMarkdownFilePageMarkersOverrideTOC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manual.md")
	content := `## Test Manual

## TABLE OF CONTENTS

| 1. MIDI CONFIG . . . | . . 12 |

<!-- page: 84 -->
## 1. MIDI CONFIG

Marker page should win.

<!-- dogear:page=85 -->
## 1.1 MIDI SYNC

Next marker should apply.

<!-- page: nope -->
## 1.2 MIDI CHANNELS

Malformed marker should be ignored and previous explicit page retained.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, chunks, err := parseMarkdownFile(path, ImportMetadata{ID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	pages := make(map[string]int64)
	for _, chunk := range chunks {
		if chunk.PageNumber.Valid {
			pages[chunk.HeadingPath] = chunk.PageNumber.Int64
		}
	}
	if pages["1. MIDI CONFIG"] != 84 {
		t.Fatalf("MIDI CONFIG page = %d, want 84", pages["1. MIDI CONFIG"])
	}
	if pages["1.1 MIDI SYNC"] != 85 {
		t.Fatalf("MIDI SYNC page = %d, want 85", pages["1.1 MIDI SYNC"])
	}
	if pages["1.2 MIDI CHANNELS"] != 85 {
		t.Fatalf("MIDI CHANNELS page = %d, want retained 85", pages["1.2 MIDI CHANNELS"])
	}
}

func TestImportSearchAndShow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".dogear", "dogear.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}

	md := filepath.Join(dir, "manual.md")
	content := `## Test Synth Manual

## TABLE OF CONTENTS

| 1. MIDI CONFIG . . . | . . 12 |

## 1. MIDI CONFIG

Turn local control off in the MIDI configuration page.
`
	if err := os.WriteFile(md, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ImportPath(context.Background(), store, md, ImportMetadata{ID: "test-synth"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Documents != 1 || result.Chunks == 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}

	results, err := store.Search(context.Background(), SearchOptions{Query: "local control", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].DocumentID != "test-synth" {
		t.Fatalf("DocumentID = %q", results[0].DocumentID)
	}

	chunks, err := store.Show(context.Background(), ShowOptions{DocumentID: "test-synth", Page: 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected page chunks")
	}

	retrieval, err := store.Retrieve(context.Background(), RetrieveOptions{Query: "How do I configure MIDI sync?", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(retrieval.Blocks) == 0 {
		t.Fatal("expected fallback retrieval results")
	}
}

func TestRetrievePrefersMIDIConfigContentOverReferenceChunks(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".dogear", "dogear.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}

	md := filepath.Join(dir, "manual.md")
	content := `## Test Manual

## MIDI

MIDI config 58

MIDI sync 58

## QUICK PERFORMANCE 28

Configure 26

<!-- page: 58 -->
## 12.3.1 SYNC

Controls how the Test Manual receives and sends MIDI clock and transport commands. Change sync settings from this menu.
`
	if err := os.WriteFile(md, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportPath(context.Background(), store, md, ImportMetadata{ID: "test"}, false); err != nil {
		t.Fatal(err)
	}

	retrieval, err := store.Retrieve(context.Background(), RetrieveOptions{Query: "How do I configure MIDI sync?", Limit: 3, Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(retrieval.Blocks) == 0 {
		t.Fatal("expected retrieval blocks")
	}
	if retrieval.Blocks[0].Source.HeadingPath != "12.3.1 SYNC" {
		t.Fatalf("top heading = %q, want sync section; debug=%#v", retrieval.Blocks[0].Source.HeadingPath, retrieval.Blocks[0].Source.Debug)
	}
}

func TestDocumentLifecycleAndRetrieve(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".dogear", "dogear.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}

	md := filepath.Join(dir, "manual.md")
	content := `## Lifecycle Manual

<!-- page: 7 -->
## Local Control

Turn local control off from the MIDI page.
`
	if err := os.WriteFile(md, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportPath(context.Background(), store, md, ImportMetadata{ID: "lifecycle", Tags: []string{"synth"}}, false); err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "lifecycle" || docs[0].ChunkCount == 0 || docs[0].IndexedChunks == 0 {
		t.Fatalf("unexpected docs: %#v", docs)
	}

	info, err := store.DocumentInfo(context.Background(), "lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	if info.PageCount != 1 || len(info.Tags) != 1 || info.Tags[0] != "synth" {
		t.Fatalf("unexpected info: %#v", info)
	}

	retrieval, err := store.Retrieve(context.Background(), RetrieveOptions{Query: "local control", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(retrieval.Blocks) == 0 || retrieval.Blocks[0].Source.DocumentID != "lifecycle" || retrieval.Blocks[0].Text == "" {
		t.Fatalf("unexpected retrieved blocks: %#v", retrieval)
	}
	if retrieval.Blocks[0].Source.Label != "[1]" {
		t.Fatalf("label = %q, want [1]", retrieval.Blocks[0].Source.Label)
	}

	if err := store.RemoveDocument(context.Background(), "lifecycle"); err != nil {
		t.Fatal(err)
	}
	docs, err = store.ListDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected no docs after remove, got %#v", docs)
	}
}
