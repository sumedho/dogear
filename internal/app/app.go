package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/llm"
)

type ProviderOverride struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout string
}

type AskOptions struct {
	Question   string
	DocumentID string
	Limit      int
	DryRun     bool
	ConfigPath string
	Provider   ProviderOverride
}

type AskResult struct {
	Answer      string
	Model       string
	ProviderURL string
	Sources     []SourceRef `json:"sources"`
	Retrieval   RetrievalResult
	DryRun      *llm.DryRun
}

type DocumentInfo struct {
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

type SearchResult struct {
	DocumentID  string     `json:"document_id"`
	Title       string     `json:"title"`
	HeadingPath string     `json:"heading_path"`
	PageNumber  *int64     `json:"page_number"`
	StartLine   int        `json:"start_line"`
	EndLine     int        `json:"end_line"`
	Snippet     string     `json:"snippet"`
	Score       float64    `json:"score"`
	Debug       *RankDebug `json:"debug,omitempty"`
}

type SourceRef struct {
	Label       string     `json:"label"`
	DocumentID  string     `json:"document_id"`
	Title       string     `json:"title"`
	Brand       string     `json:"brand,omitempty"`
	Model       string     `json:"model,omitempty"`
	HeadingPath string     `json:"heading_path"`
	PageNumber  *int64     `json:"page_number"`
	StartLine   int        `json:"start_line"`
	EndLine     int        `json:"end_line"`
	Score       float64    `json:"score"`
	Debug       *RankDebug `json:"debug,omitempty"`
}

type RankDebug struct {
	RawScore    float64  `json:"raw_score"`
	RerankScore float64  `json:"rerank_score"`
	Quality     string   `json:"quality"`
	Reasons     []string `json:"reasons"`
}

type ContextBlock struct {
	Source SourceRef `json:"source"`
	Text   string    `json:"text"`
}

type RetrievalResult struct {
	Query  string         `json:"query"`
	Blocks []ContextBlock `json:"blocks"`
}

type AskResponse struct {
	Answer      string          `json:"answer"`
	Model       string          `json:"model"`
	ProviderURL string          `json:"provider_url"`
	Sources     []SourceRef     `json:"sources"`
	Retrieval   RetrievalResult `json:"retrieval"`
}

func Ask(ctx context.Context, store *dogear.Store, opts AskOptions) (AskResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	retrieval, err := store.Retrieve(ctx, dogear.RetrieveOptions{
		Query:      opts.Question,
		DocumentID: opts.DocumentID,
		Limit:      opts.Limit,
	})
	if err != nil {
		return AskResult{}, err
	}
	if len(retrieval.Blocks) == 0 {
		return AskResult{}, fmt.Errorf("no context found for %q", opts.Question)
	}

	config, err := ProviderConfig(opts.ConfigPath, opts.Provider)
	if err != nil {
		return AskResult{}, err
	}
	messages := BuildAskMessages(retrieval)
	request := llm.BuildRequest(config.Model, messages)
	if opts.DryRun && request.Model == "" {
		request.Model = "<model>"
	}

	clientConfig := config
	if opts.DryRun && clientConfig.Model == "" {
		clientConfig.Model = "<model>"
	}
	client, err := llm.NewClient(clientConfig)
	if err != nil {
		return AskResult{}, err
	}

	result := AskResult{
		Model:       config.Model,
		ProviderURL: client.DryRun(request).URL,
		Sources:     SourceResponses(retrieval, false),
		Retrieval:   RetrievalResponse(retrieval, false),
	}
	if opts.DryRun {
		dryRun := client.DryRun(request)
		result.DryRun = &dryRun
		return result, nil
	}
	response, err := client.Chat(ctx, request)
	if err != nil {
		return AskResult{}, err
	}
	result.Answer = response.Content
	return result, nil
}

func ProviderConfig(configPath string, override ProviderOverride) (llm.Config, error) {
	fileConfig, err := llm.ConfigFromTOMLFile(configPath)
	if err != nil {
		return llm.Config{}, err
	}
	envConfig, err := llm.ConfigFromEnv()
	if err != nil {
		return llm.Config{}, err
	}
	flagConfig := llm.Config{
		BaseURL: strings.TrimSpace(override.BaseURL),
		APIKey:  strings.TrimSpace(override.APIKey),
		Model:   strings.TrimSpace(override.Model),
	}
	if override.Timeout != "" {
		timeout, err := time.ParseDuration(override.Timeout)
		if err != nil {
			return llm.Config{}, fmt.Errorf("invalid timeout: %w", err)
		}
		flagConfig.Timeout = timeout
	}
	config := llm.MergeConfig(llm.MergeConfig(fileConfig, envConfig), flagConfig)
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:11434/v1"
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	return config, nil
}

func BuildAskMessages(retrieval dogear.RetrievalResult) []llm.Message {
	return []llm.Message{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You answer questions using only the provided sources.",
				"Cite factual claims with source labels like [1] or [2].",
				"If the sources do not contain the answer, say that the sources do not contain enough information.",
			}, " "),
		},
		{
			Role:    "user",
			Content: PromptContext(retrieval),
		},
	}
}

func PromptContext(result dogear.RetrievalResult) string {
	var builder strings.Builder
	builder.WriteString("Question: ")
	builder.WriteString(result.Query)
	builder.WriteString("\n\nUse the following sources to answer. Cite sources by their labels, such as [1].\n\n")
	for _, block := range result.Blocks {
		builder.WriteString(FormatSource(block.Source))
		builder.WriteString("\n")
		builder.WriteString(block.Text)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func FormatSource(source dogear.SourceRef) string {
	parts := []string{source.Label, source.Title}
	if source.PageNumber.Valid {
		parts = append(parts, fmt.Sprintf("p.%d", source.PageNumber.Int64))
	}
	if source.HeadingPath != "" {
		parts = append(parts, source.HeadingPath)
	}
	parts = append(parts, fmt.Sprintf("lines %d-%d", source.StartLine, source.EndLine))
	return strings.Join(parts, " | ")
}

func DocumentInfoResponse(info dogear.DocumentInfo) DocumentInfo {
	tags := info.Tags
	if tags == nil {
		tags = []string{}
	}
	return DocumentInfo{
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

func DocumentInfoResponses(infos []dogear.DocumentInfo) []DocumentInfo {
	out := make([]DocumentInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, DocumentInfoResponse(info))
	}
	return out
}

func SearchResponses(results []dogear.SearchResult, includeDebug bool) []SearchResult {
	out := make([]SearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, SearchResult{
			DocumentID:  result.DocumentID,
			Title:       result.Title,
			HeadingPath: result.HeadingPath,
			PageNumber:  nullIntPtr(result.PageNumber),
			StartLine:   result.StartLine,
			EndLine:     result.EndLine,
			Snippet:     result.Snippet,
			Score:       result.Score,
			Debug:       RankDebugResponse(result.Debug, includeDebug),
		})
	}
	return out
}

func RetrievalResponse(result dogear.RetrievalResult, includeDebug bool) RetrievalResult {
	out := RetrievalResult{
		Query:  result.Query,
		Blocks: make([]ContextBlock, 0, len(result.Blocks)),
	}
	for _, block := range result.Blocks {
		out.Blocks = append(out.Blocks, ContextBlock{
			Source: SourceResponse(block.Source, includeDebug),
			Text:   block.Text,
		})
	}
	return out
}

func SourceResponses(result dogear.RetrievalResult, includeDebug bool) []SourceRef {
	sources := make([]SourceRef, 0, len(result.Blocks))
	for _, block := range result.Blocks {
		sources = append(sources, SourceResponse(block.Source, includeDebug))
	}
	return sources
}

func SourceResponse(source dogear.SourceRef, includeDebug bool) SourceRef {
	return SourceRef{
		Label:       source.Label,
		DocumentID:  source.DocumentID,
		Title:       source.Title,
		Brand:       source.Brand,
		Model:       source.Model,
		HeadingPath: source.HeadingPath,
		PageNumber:  nullIntPtr(source.PageNumber),
		StartLine:   source.StartLine,
		EndLine:     source.EndLine,
		Score:       source.Score,
		Debug:       RankDebugResponse(source.Debug, includeDebug),
	}
}

func RankDebugResponse(debug dogear.RankDebug, include bool) *RankDebug {
	if !include {
		return nil
	}
	return &RankDebug{
		RawScore:    debug.RawScore,
		RerankScore: debug.RerankScore,
		Quality:     debug.Quality,
		Reasons:     debug.Reasons,
	}
}

func nullIntPtr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
