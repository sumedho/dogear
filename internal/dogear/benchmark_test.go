package dogear

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func benchmarkStore(b *testing.B) *Store {
	b.Helper()
	store, err := Open(filepath.Join(b.TempDir(), "benchmark.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	if err := store.InitContext(context.Background()); err != nil {
		b.Fatal(err)
	}
	for documentIndex := range 100 {
		chunks := make([]Chunk, 100)
		for chunkIndex := range chunks {
			chunks[chunkIndex] = Chunk{Ordinal: chunkIndex + 1, HeadingPath: fmt.Sprintf("Section %d", chunkIndex), Text: "MIDI synchronization local control troubleshooting configuration", TextHash: fmt.Sprintf("%d-%d", documentIndex, chunkIndex)}
		}
		document := Document{ID: fmt.Sprintf("manual-%d", documentIndex), Title: fmt.Sprintf("Manual %d", documentIndex), SourcePath: "manual.md", SourceHash: fmt.Sprintf("hash-%d", documentIndex)}
		if err := store.UpsertDocument(context.Background(), document, chunks, false); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := store.RebuildIndex(context.Background()); err != nil {
		b.Fatal(err)
	}
	return store
}

func BenchmarkSearchLargeLibrary(b *testing.B) {
	store := benchmarkStore(b)
	b.ResetTimer()
	for range b.N {
		if _, err := store.Search(context.Background(), SearchOptions{Query: "MIDI local control", Limit: 8}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListDocumentsLargeLibrary(b *testing.B) {
	store := benchmarkStore(b)
	b.ResetTimer()
	for range b.N {
		if _, err := store.ListDocuments(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseMarkdown(b *testing.B) {
	content := []byte("# Manual\n\n" + strings.Repeat("## MIDI configuration\n\nSet local control and clock synchronization.\n\n", 1000))
	b.ResetTimer()
	for range b.N {
		if _, _, _, err := parseMarkdown("manual.md", content, ImportMetadata{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRebuildFTSLargeLibrary(b *testing.B) {
	store := benchmarkStore(b)
	b.ResetTimer()
	for range b.N {
		if _, err := store.RebuildIndex(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildEmbeddingsLargeLibrary(b *testing.B) {
	store := benchmarkStore(b)
	embed := func(_ context.Context, inputs []string) ([][]float32, error) {
		vectors := make([][]float32, len(inputs))
		for index := range vectors {
			vectors[index] = make([]float32, 32)
		}
		return vectors, nil
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := store.BuildEmbeddingIndex(context.Background(), "benchmark", 32, 32, "benchmark", true, embed, nil); err != nil {
			b.Fatal(err)
		}
	}
}
