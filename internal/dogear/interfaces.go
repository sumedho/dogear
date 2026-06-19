package dogear

import "context"

// SchemaStore initializes and verifies the persistence schema.
type SchemaStore interface {
	Init() error
}

// DocumentStore persists and manages imported documents.
type DocumentStore interface {
	UpsertDocument(context.Context, Document, []Chunk, bool) error
	UpsertDocumentWithImages(context.Context, Document, []Chunk, []DocumentImage, bool) error
	ListDocuments(context.Context) ([]DocumentInfo, error)
	DocumentInfo(context.Context, string) (DocumentInfo, error)
	RemoveDocument(context.Context, string) error
}

// ContentStore reads stored document content and images.
type ContentStore interface {
	Show(context.Context, ShowOptions) ([]Chunk, error)
	DocumentChunks(context.Context, string, int, int) ([]DocumentChunk, error)
	DocumentChunk(context.Context, string, int64) (DocumentChunk, error)
	Image(context.Context, int64) (StoredImage, error)
}

// EmbeddingIndexReader reports whether a vector index is usable.
type EmbeddingIndexReader interface {
	EmbeddingStatus(context.Context, string, int, string) (EmbeddingIndexStatus, error)
}

// FTSIndexStore manages the lexical search index.
type FTSIndexStore interface {
	RebuildIndex(context.Context) (int, error)
}

// RetrievalStore provides lexical and hybrid search and retrieval.
type RetrievalStore interface {
	EmbeddingIndexReader
	Retrieve(context.Context, RetrieveOptions) (RetrievalResult, error)
	RetrieveHybrid(context.Context, RetrieveOptions, []float32) (RetrievalResult, error)
	Search(context.Context, SearchOptions) ([]SearchResult, error)
	SearchHybrid(context.Context, SearchOptions, []float32) ([]SearchResult, error)
}

// EmbeddingStore manages vector index contents and lifecycle.
type EmbeddingStore interface {
	EmbeddingIndexReader
	EmbeddingChunks(context.Context) ([]EmbeddingChunk, error)
	BuildEmbeddingIndex(context.Context, string, int, int, string, bool, func(context.Context, []string) ([][]float32, error), func(int, int)) (EmbeddingIndexStatus, error)
}

// HealthStore exposes persistence diagnostics.
type HealthStore interface {
	Doctor(context.Context) (DoctorReport, error)
}

var (
	_ SchemaStore    = (*Store)(nil)
	_ DocumentStore  = (*Store)(nil)
	_ ContentStore   = (*Store)(nil)
	_ FTSIndexStore  = (*Store)(nil)
	_ RetrievalStore = (*Store)(nil)
	_ EmbeddingStore = (*Store)(nil)
	_ HealthStore    = (*Store)(nil)
)
