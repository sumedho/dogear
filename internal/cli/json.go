package cli

import (
	"encoding/json"
	"io"

	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/sqlutil"
)

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type documentInfoJSON struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Brand         string   `json:"brand,omitempty"`
	Model         string   `json:"model,omitempty"`
	Version       string   `json:"version,omitempty"`
	SourcePath    string   `json:"source_path"`
	SourceHash    string   `json:"source_hash"`
	Tags          []string `json:"tags"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	ChunkCount    int      `json:"chunk_count"`
	IndexedChunks int      `json:"indexed_chunks"`
	PageCount     int      `json:"page_count"`
}

type searchResultJSON struct {
	DocumentID  string         `json:"document_id"`
	Title       string         `json:"title"`
	HeadingPath string         `json:"heading_path"`
	PageNumber  *int64         `json:"page_number"`
	StartLine   int            `json:"start_line"`
	EndLine     int            `json:"end_line"`
	Snippet     string         `json:"snippet"`
	Score       float64        `json:"score"`
	Debug       *rankDebugJSON `json:"debug,omitempty"`
}

type chunkJSON struct {
	ID           int64  `json:"id"`
	DocumentID   string `json:"document_id"`
	Ordinal      int    `json:"ordinal"`
	HeadingPath  string `json:"heading_path"`
	HeadingLevel int    `json:"heading_level"`
	PageNumber   *int64 `json:"page_number"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
	Text         string `json:"text"`
	TextHash     string `json:"text_hash"`
}

type sourceRefJSON struct {
	ChunkID     int64          `json:"chunk_id"`
	Label       string         `json:"label"`
	DocumentID  string         `json:"document_id"`
	Title       string         `json:"title"`
	Brand       string         `json:"brand,omitempty"`
	Model       string         `json:"model,omitempty"`
	HeadingPath string         `json:"heading_path"`
	PageNumber  *int64         `json:"page_number"`
	StartLine   int            `json:"start_line"`
	EndLine     int            `json:"end_line"`
	Score       float64        `json:"score"`
	Debug       *rankDebugJSON `json:"debug,omitempty"`
}

type rankDebugJSON struct {
	RawScore       float64  `json:"raw_score"`
	RerankScore    float64  `json:"rerank_score"`
	Quality        string   `json:"quality"`
	Reasons        []string `json:"reasons"`
	Mode           string   `json:"mode,omitempty"`
	FTSRank        int      `json:"fts_rank,omitempty"`
	VectorRank     int      `json:"vector_rank,omitempty"`
	VectorDistance float64  `json:"vector_distance,omitempty"`
	FusedScore     float64  `json:"fused_score,omitempty"`
	FallbackReason string   `json:"fallback_reason,omitempty"`
}

type contextBlockJSON struct {
	Source sourceRefJSON `json:"source"`
	Text   string        `json:"text"`
}

type retrievalResultJSON struct {
	Query  string             `json:"query"`
	Blocks []contextBlockJSON `json:"blocks"`
}

type askResponseJSON struct {
	Answer      string              `json:"answer"`
	Model       string              `json:"model"`
	ProviderURL string              `json:"provider_url"`
	Sources     []sourceRefJSON     `json:"sources"`
	Retrieval   retrievalResultJSON `json:"retrieval"`
}

type doctorResponse struct {
	Database      string `json:"database"`
	SchemaVersion int    `json:"schema_version"`
	FTS5          bool   `json:"fts5"`
	Documents     int    `json:"documents"`
	Chunks        int    `json:"chunks"`
	IndexedChunks int    `json:"indexed_chunks"`
	OrphanChunks  int    `json:"orphan_chunks"`
}

func documentInfoResponses(infos []dogear.DocumentInfo) []documentInfoJSON {
	out := make([]documentInfoJSON, 0, len(infos))
	for _, info := range infos {
		out = append(out, documentInfoResponse(info))
	}
	return out
}

func documentInfoResponse(info dogear.DocumentInfo) documentInfoJSON {
	tags := info.Tags
	if tags == nil {
		tags = []string{}
	}
	return documentInfoJSON{
		ID:            info.ID,
		Title:         info.Title,
		Brand:         info.Brand,
		Model:         info.Model,
		Version:       info.Version,
		SourcePath:    info.SourcePath,
		SourceHash:    info.SourceHash,
		Tags:          tags,
		CreatedAt:     info.CreatedAt,
		UpdatedAt:     info.UpdatedAt,
		ChunkCount:    info.ChunkCount,
		IndexedChunks: info.IndexedChunks,
		PageCount:     info.PageCount,
	}
}

func searchResultResponses(results []dogear.SearchResult, includeDebug bool) []searchResultJSON {
	out := make([]searchResultJSON, 0, len(results))
	for _, result := range results {
		out = append(out, searchResultJSON{
			DocumentID:  result.DocumentID,
			Title:       result.Title,
			HeadingPath: result.HeadingPath,
			PageNumber:  sqlutil.Int64Ptr(result.PageNumber),
			StartLine:   result.StartLine,
			EndLine:     result.EndLine,
			Snippet:     result.Snippet,
			Score:       result.Score,
			Debug:       rankDebugResponse(result.Debug, includeDebug),
		})
	}
	return out
}

func chunkResponses(chunks []dogear.Chunk) []chunkJSON {
	out := make([]chunkJSON, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, chunkJSON{
			ID:           chunk.ID,
			DocumentID:   chunk.DocumentID,
			Ordinal:      chunk.Ordinal,
			HeadingPath:  chunk.HeadingPath,
			HeadingLevel: chunk.HeadingLevel,
			PageNumber:   sqlutil.Int64Ptr(chunk.PageNumber),
			StartLine:    chunk.StartLine,
			EndLine:      chunk.EndLine,
			Text:         chunk.Text,
			TextHash:     chunk.TextHash,
		})
	}
	return out
}

func retrievalResultResponse(result dogear.RetrievalResult, includeDebug bool) retrievalResultJSON {
	out := retrievalResultJSON{
		Query:  result.Query,
		Blocks: make([]contextBlockJSON, 0, len(result.Blocks)),
	}
	for _, block := range result.Blocks {
		out.Blocks = append(out.Blocks, contextBlockJSON{
			Source: sourceRefResponse(block.Source, includeDebug),
			Text:   block.Text,
		})
	}
	return out
}

func sourceRefResponse(source dogear.SourceRef, includeDebug bool) sourceRefJSON {
	return sourceRefJSON{
		ChunkID:     source.ChunkID,
		Label:       source.Label,
		DocumentID:  source.DocumentID,
		Title:       source.Title,
		Brand:       source.Brand,
		Model:       source.Model,
		HeadingPath: source.HeadingPath,
		PageNumber:  sqlutil.Int64Ptr(source.PageNumber),
		StartLine:   source.StartLine,
		EndLine:     source.EndLine,
		Score:       source.Score,
		Debug:       rankDebugResponse(source.Debug, includeDebug),
	}
}

func rankDebugResponse(debug dogear.RankDebug, include bool) *rankDebugJSON {
	if !include {
		return nil
	}
	return &rankDebugJSON{
		RawScore:    debug.RawScore,
		RerankScore: debug.RerankScore,
		Quality:     debug.Quality,
		Reasons:     debug.Reasons,
		Mode:        debug.Mode, FTSRank: debug.FTSRank, VectorRank: debug.VectorRank,
		VectorDistance: debug.VectorDistance, FusedScore: debug.FusedScore, FallbackReason: debug.FallbackReason,
	}
}
