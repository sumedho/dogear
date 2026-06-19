package dogear

import (
	"database/sql"
)

type Document struct {
	ID         string
	Title      string
	Brand      string
	Model      string
	Version    string
	SourcePath string
	SourceHash string
	Tags       []string
}

type DocumentInfo struct {
	Document
	CreatedAt     string
	UpdatedAt     string
	ChunkCount    int
	IndexedChunks int
	PageCount     int
}

type Chunk struct {
	ID           int64
	DocumentID   string
	Ordinal      int
	HeadingPath  string
	HeadingLevel int
	PageNumber   sql.NullInt64
	StartLine    int
	EndLine      int
	Text         string
	TextHash     string
}

type DocumentChunk struct {
	Chunk
	Images []ImageRef
}

type SearchOptions struct {
	Query      string
	DocumentID string
	Limit      int
	Debug      bool
}

type SearchResult struct {
	ChunkID     int64
	DocumentID  string
	Title       string
	HeadingPath string
	PageNumber  sql.NullInt64
	StartLine   int
	EndLine     int
	Snippet     string
	Score       float64
	Debug       RankDebug
}

type RetrieveOptions struct {
	Query      string
	DocumentID string
	Limit      int
	Debug      bool
}

type RetrievedChunk struct {
	ChunkID     int64
	DocumentID  string
	Title       string
	Brand       string
	Model       string
	HeadingPath string
	PageNumber  sql.NullInt64
	StartLine   int
	EndLine     int
	Text        string
	Score       float64
	Debug       RankDebug
}

type SourceRef struct {
	ChunkID     int64
	Label       string
	DocumentID  string
	Title       string
	Brand       string
	Model       string
	HeadingPath string
	PageNumber  sql.NullInt64
	StartLine   int
	EndLine     int
	Score       float64
	Debug       RankDebug
}

type ContextBlock struct {
	Source SourceRef
	Text   string
	Images []ImageRef
}

type ImageRef struct {
	ID        int64
	Alt       string
	MediaType string
}

type StoredImage struct {
	ImageRef
	Data        []byte
	ContentHash string
}

type RetrievalResult struct {
	Query  string
	Blocks []ContextBlock
}

type RankDebug struct {
	RawScore       float64
	RerankScore    float64
	Quality        string
	Reasons        []string
	Mode           string
	FTSRank        int
	VectorRank     int
	VectorDistance float64
	FusedScore     float64
	FallbackReason string
}

type ShowOptions struct {
	DocumentID string
	Page       int
	Section    string
}

type DoctorReport struct {
	SchemaVersion int
	FTS5          bool
	Documents     int
	Chunks        int
	IndexedChunks int
	OrphanChunks  int
}

type EmbeddingIndexStatus struct {
	Configured bool   `json:"configured"`
	Complete   bool   `json:"complete"`
	Stale      bool   `json:"stale"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	Indexed    int    `json:"indexed"`
	Total      int    `json:"total"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type EmbeddingChunk struct {
	ID         int64
	DocumentID string
	Title      string
	Heading    string
	Text       string
	TextHash   string
}
