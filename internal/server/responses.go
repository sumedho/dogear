package server

import (
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/sqlutil"
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
	Images      []imageRefResponse `json:"images,omitempty"`
	Score       float64            `json:"score"`
	Debug       *rankDebugResponse `json:"debug,omitempty"`
}

type sourceRefResponse struct {
	ChunkID     int64              `json:"chunk_id"`
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

type contextBlockResponse struct {
	Source sourceRefResponse  `json:"source"`
	Text   string             `json:"text"`
	Images []imageRefResponse `json:"images,omitempty"`
}

type retrievalResultResponse struct {
	Query  string                 `json:"query"`
	Blocks []contextBlockResponse `json:"blocks"`
}

type documentChunkResponse struct {
	ID           int64              `json:"id"`
	DocumentID   string             `json:"document_id"`
	Ordinal      int                `json:"ordinal"`
	HeadingPath  string             `json:"heading_path"`
	HeadingLevel int                `json:"heading_level"`
	PageNumber   *int64             `json:"page_number"`
	StartLine    int                `json:"start_line"`
	EndLine      int                `json:"end_line"`
	Text         string             `json:"text"`
	Images       []imageRefResponse `json:"images,omitempty"`
}

type imageRefResponse struct {
	ID        int64  `json:"id"`
	Alt       string `json:"alt"`
	MediaType string `json:"media_type"`
}

func documentChunkResponses(chunks []dogear.DocumentChunk) []documentChunkResponse {
	out := make([]documentChunkResponse, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, documentChunkResponseFor(chunk))
	}
	return out
}

func documentChunkResponseFor(chunk dogear.DocumentChunk) documentChunkResponse {
	images := imageRefResponses(chunk.Images)
	return documentChunkResponse{ID: chunk.ID, DocumentID: chunk.DocumentID, Ordinal: chunk.Ordinal, HeadingPath: chunk.HeadingPath,
		HeadingLevel: chunk.HeadingLevel, PageNumber: sqlutil.Int64Ptr(chunk.PageNumber), StartLine: chunk.StartLine, EndLine: chunk.EndLine, Text: chunk.Text, Images: images}
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
			PageNumber: sqlutil.Int64Ptr(result.PageNumber), StartLine: result.StartLine, EndLine: result.EndLine,
			Snippet: result.Snippet, Images: imageRefResponses(result.Images), Score: result.Score, Debug: rankDebugResponseFor(result.Debug, includeDebug),
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
			Images: imageRefResponses(block.Images),
		})
	}
	return out
}

func imageRefResponses(images []dogear.ImageRef) []imageRefResponse {
	out := make([]imageRefResponse, 0, len(images))
	for _, image := range images {
		out = append(out, imageRefResponse{ID: image.ID, Alt: image.Alt, MediaType: image.MediaType})
	}
	return out
}

func sourceRefResponseFor(source dogear.SourceRef, includeDebug bool) sourceRefResponse {
	return sourceRefResponse{
		ChunkID: source.ChunkID, Label: source.Label, DocumentID: source.DocumentID, Title: source.Title,
		Brand: source.Brand, Model: source.Model, HeadingPath: source.HeadingPath,
		PageNumber: sqlutil.Int64Ptr(source.PageNumber), StartLine: source.StartLine, EndLine: source.EndLine,
		Score: source.Score, Debug: rankDebugResponseFor(source.Debug, includeDebug),
	}
}

func rankDebugResponseFor(debug dogear.RankDebug, include bool) *rankDebugResponse {
	if !include {
		return nil
	}
	return &rankDebugResponse{
		RawScore: debug.RawScore, RerankScore: debug.RerankScore,
		Quality: debug.Quality, Reasons: debug.Reasons, Mode: debug.Mode,
		FTSRank: debug.FTSRank, VectorRank: debug.VectorRank, VectorDistance: debug.VectorDistance,
		FusedScore: debug.FusedScore, FallbackReason: debug.FallbackReason,
	}
}
