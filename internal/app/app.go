package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/sumedho/dogear/internal/llm"
	"github.com/sumedho/dogear/internal/retrievalpolicy"
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
	History    []ConversationMessage
}

type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type RetrieveOptions struct {
	Query      string
	DocumentID string
	Limit      int
}

type Retriever interface {
	Retrieve(context.Context, RetrieveOptions) (RetrievalResult, error)
}

type AskResult struct {
	Answer      string
	Model       string
	ProviderURL string
	Sources     []SourceRef `json:"sources"`
	Retrieval   RetrievalResult
	Images      []DisplayImage `json:"images,omitempty"`
	DryRun      *llm.DryRun
}

type SourceRef struct {
	ChunkID     int64   `json:"chunk_id"`
	Label       string  `json:"label"`
	DocumentID  string  `json:"document_id"`
	Title       string  `json:"title"`
	Brand       string  `json:"brand,omitempty"`
	Model       string  `json:"model,omitempty"`
	HeadingPath string  `json:"heading_path"`
	PageNumber  *int64  `json:"page_number"`
	StartLine   int     `json:"start_line"`
	EndLine     int     `json:"end_line"`
	Score       float64 `json:"score"`
}

type ContextBlock struct {
	Source SourceRef  `json:"source"`
	Text   string     `json:"text"`
	Images []ImageRef `json:"images,omitempty"`
}

type ImageRef struct {
	ID        int64  `json:"id"`
	Alt       string `json:"alt"`
	MediaType string `json:"media_type"`
}

type DisplayImage struct {
	ID        int64     `json:"id"`
	Alt       string    `json:"alt"`
	MediaType string    `json:"media_type"`
	Source    SourceRef `json:"source"`
}

type RetrievalResult struct {
	Query          string         `json:"query"`
	Mode           string         `json:"mode,omitempty"`
	FallbackReason string         `json:"fallback_reason,omitempty"`
	Blocks         []ContextBlock `json:"blocks"`
}

type AskResponse struct {
	Answer      string          `json:"answer"`
	Model       string          `json:"model"`
	ProviderURL string          `json:"provider_url"`
	Sources     []SourceRef     `json:"sources"`
	Retrieval   RetrievalResult `json:"retrieval"`
	Images      []DisplayImage  `json:"images,omitempty"`
}

func Ask(ctx context.Context, retriever Retriever, opts AskOptions) (AskResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = retrievalpolicy.DefaultContextLimit
	}
	retrieval, err := retriever.Retrieve(ctx, RetrieveOptions{
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
	messages := BuildAskMessagesWithHistory(retrieval, opts.History)
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
		Sources:     sourceRefs(retrieval),
		Retrieval:   retrieval,
		Images:      displayImages(opts.Question, retrieval),
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

func AskStream(ctx context.Context, retriever Retriever, opts AskOptions, onDelta func(string) error) (AskResult, error) {
	if opts.DryRun {
		return Ask(ctx, retriever, opts)
	}
	if opts.Limit <= 0 {
		opts.Limit = retrievalpolicy.DefaultContextLimit
	}
	retrieval, err := retriever.Retrieve(ctx, RetrieveOptions{Query: opts.Question, DocumentID: opts.DocumentID, Limit: opts.Limit})
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
	client, err := llm.NewClient(config)
	if err != nil {
		return AskResult{}, err
	}
	request := llm.BuildRequest(config.Model, BuildAskMessagesWithHistory(retrieval, opts.History))
	response, err := client.ChatStream(ctx, request, onDelta)
	if err != nil {
		return AskResult{}, err
	}
	return AskResult{
		Answer: response.Content, Model: config.Model, ProviderURL: client.DryRun(request).URL,
		Sources: sourceRefs(retrieval), Retrieval: retrieval, Images: displayImages(opts.Question, retrieval),
	}, nil
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

func BuildAskMessages(retrieval RetrievalResult) []llm.Message {
	return BuildAskMessagesWithHistory(retrieval, nil)
}

func BuildAskMessagesWithHistory(retrieval RetrievalResult, history []ConversationMessage) []llm.Message {
	messages := []llm.Message{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You answer questions using only the provided sources.",
				"Cite factual claims with source labels like [1] or [2].",
				"Source image metadata may be provided; you cannot inspect image pixels, but the application can display listed images.",
				"If the user asks to show or display a relevant listed image, say that it is displayed below and cite its source; do not claim that the image is unavailable.",
				"Do not infer visual details beyond the source text and image alt text.",
				"If the sources do not contain the answer, say that the sources do not contain enough information.",
			}, " "),
		},
	}
	for _, message := range history {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if (role != "user" && role != "assistant") || content == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: role, Content: content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: PromptContext(retrieval)})
	return messages
}

func PromptContext(result RetrievalResult) string {
	var builder strings.Builder
	builder.WriteString("Question: ")
	builder.WriteString(result.Query)
	builder.WriteString("\n\nUse the following sources to answer. Cite sources by their labels, such as [1].\n\n")
	for _, block := range result.Blocks {
		builder.WriteString(FormatSource(block.Source))
		builder.WriteString("\n")
		builder.WriteString(block.Text)
		if len(block.Images) > 0 {
			builder.WriteString("\nAvailable source images:\n")
			for _, image := range block.Images {
				fmt.Fprintf(&builder, "- image %d: %q (%s) from %s\n", image.ID, image.Alt, image.MediaType, block.Source.Label)
			}
		}
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func displayImages(question string, result RetrievalResult) []DisplayImage {
	if !wantsImages(question) {
		return nil
	}
	seen := make(map[int64]bool)
	var images []DisplayImage
	for _, block := range result.Blocks {
		for _, image := range block.Images {
			if seen[image.ID] {
				continue
			}
			seen[image.ID] = true
			images = append(images, DisplayImage{ID: image.ID, Alt: image.Alt, MediaType: image.MediaType, Source: block.Source})
		}
	}
	return images
}

func wantsImages(question string) bool {
	question = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, question)
	question = " " + strings.Join(strings.Fields(question), " ") + " "
	for _, term := range []string{
		"image", "picture", "photo", "diagram", "schematic", "illustration", "figure",
		"front panel", "rear panel", "back panel", "panel layout", "visual reference",
	} {
		if strings.Contains(question, " "+term+" ") {
			return true
		}
	}
	return false
}

func FormatSource(source SourceRef) string {
	parts := []string{source.Label, source.Title}
	if source.PageNumber != nil {
		parts = append(parts, fmt.Sprintf("p.%d", *source.PageNumber))
	}
	if source.HeadingPath != "" {
		parts = append(parts, source.HeadingPath)
	}
	parts = append(parts, fmt.Sprintf("lines %d-%d", source.StartLine, source.EndLine))
	return strings.Join(parts, " | ")
}

func sourceRefs(result RetrievalResult) []SourceRef {
	sources := make([]SourceRef, 0, len(result.Blocks))
	for _, block := range result.Blocks {
		sources = append(sources, block.Source)
	}
	return sources
}
