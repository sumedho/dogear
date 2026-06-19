package server

import (
	"database/sql"

	"github.com/sumedho/dogear/internal/dogear"
)

type documentInfoResponse struct {
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

type searchResultResponse struct {
	DocumentID  string             `json:"document_id"`
	Title       string             `json:"title"`
	HeadingPath string             `json:"heading_path"`
	PageNumber  *int64             `json:"page_number"`
	StartLine   int                `json:"start_line"`
	EndLine     int                `json:"end_line"`
	Snippet     string             `json:"snippet"`
	Score       float64            `json:"score"`
	Debug       *rankDebugResponse `json:"debug,omitempty"`
}

type sourceRefResponse struct {
	Label       string             `json:"label"`
	DocumentID  string             `json:"document_id"`
	Title       string             `json:"title"`
	Brand       string             `json:"brand,omitempty"`
	Model       string             `json:"model,omitempty"`
	HeadingPath string             `json:"heading_path"`
	PageNumber  *int64             `json:"page_number"`
	StartLine   int                `json:"start_line"`
	EndLine     int                `json:"end_line"`
	Score       float64            `json:"score"`
	Debug       *rankDebugResponse `json:"debug,omitempty"`
}

type rankDebugResponse struct {
	RawScore    float64  `json:"raw_score"`
	RerankScore float64  `json:"rerank_score"`
	Quality     string   `json:"quality"`
	Reasons     []string `json:"reasons"`
}

type contextBlockResponse struct {
	Source sourceRefResponse `json:"source"`
	Text   string            `json:"text"`
}

type retrievalResultResponse struct {
	Query  string                 `json:"query"`
	Blocks []contextBlockResponse `json:"blocks"`
}

func documentInfoResponses(infos []dogear.DocumentInfo) []documentInfoResponse {
	out := make([]documentInfoResponse, 0, len(infos))
	for _, info := range infos {
		out = append(out, documentInfoResponseFor(info))
	}
	return out
}

func documentInfoResponseFor(info dogear.DocumentInfo) documentInfoResponse {
	tags := info.Tags
	if tags == nil {
		tags = []string{}
	}
	return documentInfoResponse{
		ID: info.ID, Title: info.Title, Brand: info.Brand, Model: info.Model,
		Version: info.Version, SourcePath: info.SourcePath, SourceHash: info.SourceHash,
		Tags: tags, CreatedAt: info.CreatedAt, UpdatedAt: info.UpdatedAt,
		ChunkCount: info.ChunkCount, IndexedChunks: info.IndexedChunks, PageCount: info.PageCount,
	}
}

func searchResultResponses(results []dogear.SearchResult, includeDebug bool) []searchResultResponse {
	out := make([]searchResultResponse, 0, len(results))
	for _, result := range results {
		out = append(out, searchResultResponse{
			DocumentID: result.DocumentID, Title: result.Title, HeadingPath: result.HeadingPath,
			PageNumber: nullIntPtr(result.PageNumber), StartLine: result.StartLine, EndLine: result.EndLine,
			Snippet: result.Snippet, Score: result.Score, Debug: rankDebugResponseFor(result.Debug, includeDebug),
		})
	}
	return out
}

func retrievalResultResponseFor(result dogear.RetrievalResult, includeDebug bool) retrievalResultResponse {
	out := retrievalResultResponse{Query: result.Query, Blocks: make([]contextBlockResponse, 0, len(result.Blocks))}
	for _, block := range result.Blocks {
		out.Blocks = append(out.Blocks, contextBlockResponse{
			Source: sourceRefResponseFor(block.Source, includeDebug),
			Text:   block.Text,
		})
	}
	return out
}

func sourceRefResponseFor(source dogear.SourceRef, includeDebug bool) sourceRefResponse {
	return sourceRefResponse{
		Label: source.Label, DocumentID: source.DocumentID, Title: source.Title,
		Brand: source.Brand, Model: source.Model, HeadingPath: source.HeadingPath,
		PageNumber: nullIntPtr(source.PageNumber), StartLine: source.StartLine, EndLine: source.EndLine,
		Score: source.Score, Debug: rankDebugResponseFor(source.Debug, includeDebug),
	}
}

func rankDebugResponseFor(debug dogear.RankDebug, include bool) *rankDebugResponse {
	if !include {
		return nil
	}
	return &rankDebugResponse{
		RawScore: debug.RawScore, RerankScore: debug.RerankScore,
		Quality: debug.Quality, Reasons: debug.Reasons,
	}
}

func nullIntPtr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
