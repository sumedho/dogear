package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sumedho/dogear/internal/llm"
	"github.com/sumedho/dogear/internal/retrievalpolicy"
)

const (
	guideMaxSections     = retrievalpolicy.GuideMaxSections
	guideMaxQueries      = retrievalpolicy.GuideMaxQueries
	guideResultsPerQuery = retrievalpolicy.GuideResultsPerQuery
	guideMaxBlocks       = retrievalpolicy.GuideMaxBlocks
	guideMaxContextChars = retrievalpolicy.GuideMaxContextChars
)

type guidePlanPayload struct {
	Title    string `json:"title"`
	Sections []struct {
		Heading string   `json:"heading"`
		Queries []string `json:"queries"`
	} `json:"sections"`
}

type adjacentRetriever interface {
	Adjacent(context.Context, SourceRef, int) ([]ContextBlock, error)
}

func resolveResponseMode(mode ResponseMode, question string) ResponseMode {
	switch mode {
	case ResponseModeGuide:
		return ResponseModeGuide
	case ResponseModeAnswer:
		return ResponseModeAnswer
	default:
		if isGuideIntent(question) {
			return ResponseModeGuide
		}
		return ResponseModeAnswer
	}
}

func isGuideIntent(question string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, question)
	normalized = " " + strings.Join(strings.Fields(normalized), " ") + " "
	for _, phrase := range []string{" how do i ", " how to ", " walk me through ", " step by step ", " setup guide ", " set up ", " create a guide ", " write a guide ", " instructions for "} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func askGuide(ctx context.Context, retriever Retriever, opts AskOptions, onDelta func(string) error) (AskResult, error) {
	config, err := ProviderConfig(opts.ConfigPath, opts.Provider)
	if err != nil {
		return AskResult{}, err
	}
	if opts.DryRun && config.Model == "" {
		config.Model = "<model>"
	}
	client, err := llm.NewClient(config)
	if err != nil {
		return AskResult{}, err
	}
	if err := reportGuideStatus(opts, "Planning guide…"); err != nil {
		return AskResult{}, err
	}
	plan := fallbackGuidePlan(opts.Question)
	if !opts.DryRun {
		if planned, planErr := planGuide(ctx, client, config.Model, opts.Question); planErr == nil {
			plan = planned
		}
	}
	retrieval, err := retrieveGuide(ctx, retriever, opts, plan)
	if err != nil {
		return AskResult{}, err
	}
	request := llm.BuildRequest(config.Model, BuildGuideMessages(retrieval, opts.History))
	result := AskResult{Model: config.Model, ProviderURL: client.DryRun(request).URL, Sources: sourceRefs(retrieval), Retrieval: retrieval, Images: displayImages(opts.Question, retrieval), Mode: ResponseModeGuide}
	if opts.DryRun {
		dry := client.DryRun(request)
		result.DryRun = &dry
		return result, nil
	}
	if err := reportGuideStatus(opts, "Writing cited guide…"); err != nil {
		return AskResult{}, err
	}
	if onDelta == nil {
		response, err := client.Chat(ctx, request)
		if err != nil {
			return AskResult{}, err
		}
		result.Answer = response.Content
		return result, nil
	}
	response, err := client.ChatStream(ctx, request, onDelta)
	if err != nil {
		return AskResult{}, err
	}
	result.Answer = response.Content
	return result, nil
}

func planGuide(ctx context.Context, client *llm.Client, model, question string) (GuideContext, error) {
	prompt := `Return only JSON in this shape: {"title":"...","sections":[{"heading":"...","queries":["..."]}]}. Plan a practical manual-based guide for the user's question. Use no more than six sections and two focused retrieval queries per section. Include prerequisites, ordered setup or procedure, verification, and troubleshooting only when relevant. Do not answer the question.`
	request := llm.BuildRequest(model, []llm.Message{{Role: "system", Content: prompt}, {Role: "user", Content: question}})
	response, err := client.Chat(ctx, request)
	if err != nil {
		return GuideContext{}, err
	}
	return parseGuidePlan(response.Content, question)
}

func parseGuidePlan(raw, question string) (GuideContext, error) {
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return GuideContext{}, fmt.Errorf("guide plan was not JSON")
	}
	var payload guidePlanPayload
	if err := json.Unmarshal([]byte(raw[start:end+1]), &payload); err != nil {
		return GuideContext{}, err
	}
	plan := GuideContext{Title: strings.TrimSpace(payload.Title)}
	queryCount := 0
	seen := map[string]bool{}
	for _, input := range payload.Sections {
		if len(plan.Sections) >= guideMaxSections || queryCount >= guideMaxQueries {
			break
		}
		section := GuideSection{Heading: strings.TrimSpace(input.Heading)}
		if section.Heading == "" {
			continue
		}
		for _, rawQuery := range input.Queries {
			query := strings.TrimSpace(rawQuery)
			key := strings.ToLower(query)
			if query == "" || seen[key] || len(section.Queries) >= 2 || queryCount >= guideMaxQueries {
				continue
			}
			seen[key] = true
			section.Queries = append(section.Queries, query)
			queryCount++
		}
		if len(section.Queries) > 0 {
			plan.Sections = append(plan.Sections, section)
		}
	}
	if plan.Title == "" {
		plan.Title = "Guide"
	}
	if len(plan.Sections) == 0 {
		return GuideContext{}, fmt.Errorf("guide plan contained no usable sections")
	}
	_ = question
	return plan, nil
}

func fallbackGuidePlan(question string) GuideContext {
	return GuideContext{Title: "Practical guide", Sections: []GuideSection{
		{Heading: "Prerequisites", Queries: []string{question + " prerequisites requirements"}},
		{Heading: "Setup and procedure", Queries: []string{question, question + " setup steps configuration"}},
		{Heading: "Verification", Queries: []string{question + " verify test confirm"}},
		{Heading: "Troubleshooting", Queries: []string{question + " troubleshooting warning problem"}},
	}}
}

func retrieveGuide(ctx context.Context, retriever Retriever, opts AskOptions, plan GuideContext) (RetrievalResult, error) {
	result := RetrievalResult{Query: opts.Question, Guide: &plan}
	seen := map[int64]bool{}
	contextChars := 0
	var lastErr error
	for sectionIndex := range result.Guide.Sections {
		section := &result.Guide.Sections[sectionIndex]
		if err := reportGuideStatus(opts, fmt.Sprintf("Gathering %s sources…", strings.ToLower(section.Heading))); err != nil {
			return RetrievalResult{}, err
		}
		for _, query := range section.Queries {
			retrieval, err := retriever.Retrieve(ctx, RetrieveOptions{Query: query, DocumentID: opts.DocumentID, Limit: guideResultsPerQuery})
			if err != nil {
				lastErr = err
				continue
			}
			if result.Mode == "" {
				result.Mode = retrieval.Mode
			}
			if result.FallbackReason == "" {
				result.FallbackReason = retrieval.FallbackReason
			}
			for _, block := range retrieval.Blocks {
				if seen[block.Source.ChunkID] || len(result.Blocks) >= guideMaxBlocks {
					continue
				}
				remaining := guideMaxContextChars - contextChars
				if remaining <= 0 {
					break
				}
				block.Text = truncateGuideText(block.Text, remaining)
				seen[block.Source.ChunkID] = true
				block.Source.Label = fmt.Sprintf("[%d]", len(result.Blocks)+1)
				section.SourceLabels = append(section.SourceLabels, block.Source.Label)
				contextChars += len(block.Text)
				result.Blocks = append(result.Blocks, block)
				if adjacent, ok := retriever.(adjacentRetriever); ok && len(result.Blocks) < guideMaxBlocks && contextChars < guideMaxContextChars {
					neighbors, adjacentErr := adjacent.Adjacent(ctx, block.Source, 1)
					if adjacentErr == nil {
						for _, neighbor := range neighbors {
							if seen[neighbor.Source.ChunkID] {
								continue
							}
							remaining := guideMaxContextChars - contextChars
							if remaining <= 0 {
								break
							}
							neighbor.Text = truncateGuideText(neighbor.Text, remaining)
							seen[neighbor.Source.ChunkID] = true
							neighbor.Source.Label = fmt.Sprintf("[%d]", len(result.Blocks)+1)
							section.SourceLabels = append(section.SourceLabels, neighbor.Source.Label)
							contextChars += len(neighbor.Text)
							result.Blocks = append(result.Blocks, neighbor)
							break
						}
					}
				}
			}
		}
	}
	if len(result.Blocks) == 0 {
		if lastErr != nil {
			return RetrievalResult{}, lastErr
		}
		return RetrievalResult{}, fmt.Errorf("no context found for %q", opts.Question)
	}
	return result, nil
}

func BuildGuideMessages(retrieval RetrievalResult, history []ConversationMessage) []llm.Message {
	system := strings.Join([]string{
		"Create a practical guide using only the provided sources.",
		"Use clear headings, prerequisites when supported, numbered steps, warnings, verification, and troubleshooting when supported.",
		"Cite every factual step with source labels like [1] or [2].",
		"Never merge conflicting instructions; identify the conflict and which source each instruction comes from.",
		"Omit unsupported sections or state that the sources do not contain enough information.",
	}, " ")
	messages := []llm.Message{{Role: "system", Content: system}}
	for _, message := range history {
		role, content := strings.ToLower(strings.TrimSpace(message.Role)), strings.TrimSpace(message.Content)
		if (role == "user" || role == "assistant") && content != "" {
			messages = append(messages, llm.Message{Role: role, Content: content})
		}
	}
	messages = append(messages, llm.Message{Role: "user", Content: PromptGuideContext(retrieval)})
	return messages
}

func PromptGuideContext(result RetrievalResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Question: %s\n\n", result.Query)
	if result.Guide != nil {
		fmt.Fprintf(&builder, "Guide title: %s\n", result.Guide.Title)
		for _, section := range result.Guide.Sections {
			fmt.Fprintf(&builder, "- %s: evidence %s\n", section.Heading, strings.Join(section.SourceLabels, ", "))
		}
	}
	builder.WriteString("\nSources:\n\n")
	for _, block := range result.Blocks {
		builder.WriteString(FormatSource(block.Source))
		builder.WriteString("\n")
		builder.WriteString(block.Text)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func reportGuideStatus(opts AskOptions, status string) error {
	if opts.OnStatus != nil {
		return opts.OnStatus(status)
	}
	return nil
}

func truncateGuideText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
