package dogear

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

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

func TestImportMarkdownStoresEmbeddedImagesWithRetrievedChunk(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Image Manual\n\n## Local Control\n\nTurn local control off here.\n\n![Signal flow schematic](data:image/png;base64," + testPNGBase64 + ")\n")
	if _, err := ImportMarkdown(context.Background(), store, "manual.md", content, ImportMetadata{ID: "image-manual"}, false); err != nil {
		t.Fatal(err)
	}
	results, err := store.Search(context.Background(), SearchOptions{Query: "signal flow schematic", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Images) != 1 || results[0].Images[0].Alt != "Signal flow schematic" {
		t.Fatalf("image alt search returned %#v", results)
	}
	retrieval, err := store.Retrieve(context.Background(), RetrieveOptions{Query: "signal flow schematic", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(retrieval.Blocks) == 0 || len(retrieval.Blocks[0].Images) != 1 {
		t.Fatalf("unexpected retrieval: %#v", retrieval)
	}
	ref := retrieval.Blocks[0].Images[0]
	if ref.Alt != "Signal flow schematic" || ref.MediaType != "image/png" {
		t.Fatalf("unexpected image ref: %#v", ref)
	}
	embeddingChunks, err := store.EmbeddingChunks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(embeddingChunks) != 1 || !strings.Contains(embeddingChunks[0].Text, "Signal flow schematic") {
		t.Fatalf("image alt missing from embedding input: %#v", embeddingChunks)
	}
	stored, err := store.Image(context.Background(), ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := base64.StdEncoding.DecodeString(testPNGBase64)
	if string(stored.Data) != string(want) || stored.ContentHash == "" {
		t.Fatalf("unexpected stored image: %#v", stored)
	}
}

func TestImportMarkdownStoresWrappedEmbeddedImageWithSurroundingMarkup(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	mid := len(testPNGBase64) / 2
	content := []byte("# Image Manual\n\n## Diagram\n\nBefore <span>![Image](<data:image/png;base64," + testPNGBase64[:mid] + "\n  " + testPNGBase64[mid:] + "> \"Diagram\")</span> after.\n")
	if _, err := ImportMarkdown(context.Background(), store, "manual.md", content, ImportMetadata{ID: "wrapped-image"}, false); err != nil {
		t.Fatal(err)
	}
	chunks, err := store.DocumentChunks(context.Background(), "wrapped-image", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || len(chunks[0].Images) != 1 {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
	if chunks[0].Images[0].Alt != "Image" || chunks[0].Images[0].MediaType != "image/png" {
		t.Fatalf("unexpected image: %#v", chunks[0].Images[0])
	}
	if strings.Contains(chunks[0].Text, "data:image") || !strings.Contains(chunks[0].Text, "Before <span>") || !strings.Contains(chunks[0].Text, "</span> after.") {
		t.Fatalf("embedded image was not removed cleanly: %q", chunks[0].Text)
	}
}

func TestDocumentHealthRetainsAndReplacesImportWarnings(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Manual\n\n## Setup\n\nUseful setup instructions.\n\n![](data:image/png;base64," + testPNGBase64 + ")\n\n![Vector](data:image/svg+xml;base64,PHN2Zy8+)\n")
	result, err := ImportMarkdown(context.Background(), store, "manual.md", content, ImportMetadata{ID: "health-manual"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Images != 1 || len(result.Warnings) != 2 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	health, err := store.DocumentHealth(context.Background(), "health-manual", EmbeddingIndexStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if health.ChunkCount != 1 || health.ImageCount != 1 || !health.FTS.Complete || len(health.Warnings) != 2 {
		t.Fatalf("unexpected health: %#v", health)
	}
	if health.Vectors.Configured || health.Vectors.Complete {
		t.Fatalf("unexpected vector health: %#v", health.Vectors)
	}

	replacement := []byte("# Manual\n\n## Setup\n\nReplacement instructions with no import warnings.\n")
	if _, err := ImportMarkdown(context.Background(), store, "manual.md", replacement, ImportMetadata{ID: "health-manual"}, true); err != nil {
		t.Fatal(err)
	}
	health, err = store.DocumentHealth(context.Background(), "health-manual", EmbeddingIndexStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if health.ImageCount != 0 || len(health.Warnings) != 0 {
		t.Fatalf("replacement retained stale health: %#v", health)
	}
}

func TestInitMigratesExistingImageSearchIndex(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Image Manual\n\n## Routing\n\nThis section contains an illustration.\n\n![Rare waveform diagram](data:image/png;base64," + testPNGBase64 + ")\n")
	if _, err := ImportMarkdown(context.Background(), store, "manual.md", content, ImportMetadata{ID: "migration-image"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM chunks_fts;
		INSERT INTO chunks_fts(chunk_id, document_id, title, brand, model, heading_path, text)
		SELECT c.id, c.document_id, d.title, d.brand, d.model, c.heading_path, c.text FROM chunks c JOIN documents d ON d.id = c.document_id;
		DELETE FROM schema_migrations WHERE version IN (4, 5);`); err != nil {
		t.Fatal(err)
	}
	before, err := store.Search(context.Background(), SearchOptions{Query: "rare waveform diagram", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("legacy index unexpectedly found image alt text: %#v", before)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	after, err := store.Search(context.Background(), SearchOptions{Query: "rare waveform diagram", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || len(after[0].Images) != 1 {
		t.Fatalf("migrated image search returned %#v", after)
	}
	report, err := store.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != schemaVersion {
		t.Fatalf("schema version = %d, want %d", report.SchemaVersion, schemaVersion)
	}
}

func TestImportMarkdownRejectsInvalidEmbeddedImage(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Manual\n\n## Setup\n\nText.\n\n![Wrong](data:image/jpeg;base64," + testPNGBase64 + ")\n")
	if _, err := ImportMarkdown(context.Background(), store, "manual.md", content, ImportMetadata{}, false); err == nil {
		t.Fatal("ImportMarkdown() error = nil, want MIME mismatch")
	}
}

func TestEmbeddingIndexAndHybridRetrieval(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Manual\n\n## MIDI Sync\n\nConfigure MIDI clock synchronization here with transport controls.\n\n## Audio\n\nAdjust output volume and speaker balance from this menu.\n")
	if _, err := ImportMarkdown(context.Background(), store, "manual.md", content, ImportMetadata{ID: "manual"}, false); err != nil {
		t.Fatal(err)
	}
	embed := func(_ context.Context, input []string) ([][]float32, error) {
		out := make([][]float32, len(input))
		for i, text := range input {
			out[i] = make([]float32, 32)
			if strings.Contains(strings.ToLower(text), "midi") {
				out[i][0] = 1
			} else {
				out[i][1] = 1
			}
		}
		return out, nil
	}
	status, err := store.BuildEmbeddingIndex(context.Background(), "test-embedding", 32, 8, "hash", false, embed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Complete || status.Indexed != 2 {
		t.Fatalf("unexpected status: %#v", status)
	}
	health, err := store.DocumentHealth(context.Background(), "manual", status)
	if err != nil {
		t.Fatal(err)
	}
	if !health.Vectors.Complete || health.Vectors.Indexed != health.Vectors.Total || health.Vectors.Total != 2 {
		t.Fatalf("unexpected document vector health: %#v", health.Vectors)
	}
	query := make([]float32, 32)
	query[0] = 1
	result, err := store.RetrieveHybrid(context.Background(), RetrieveOptions{Query: "clock synchronization", DocumentID: "manual", Limit: 2}, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) == 0 || !strings.Contains(result.Blocks[0].Source.HeadingPath, "MIDI Sync") || result.Blocks[0].Source.Debug.Mode != "hybrid" {
		t.Fatalf("unexpected hybrid result: %#v", result)
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
