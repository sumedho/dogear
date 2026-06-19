package cli

import (
	"fmt"
	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
	"io"
	"strings"
)

func formatSource(source dogear.SourceRef) string {
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

func formatAppSource(source app.SourceRef) string {
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

func formatSearchSource(result dogear.SearchResult, index int) string {
	source := dogear.SourceRef{
		Label:       fmt.Sprintf("[%d]", index),
		DocumentID:  result.DocumentID,
		Title:       result.Title,
		HeadingPath: result.HeadingPath,
		PageNumber:  result.PageNumber,
		StartLine:   result.StartLine,
		EndLine:     result.EndLine,
		Score:       result.Score,
	}
	return formatSource(source)
}

func writePromptContext(out io.Writer, result dogear.RetrievalResult) error {
	if _, err := fmt.Fprintf(out, "Question: %s\n\nUse the following sources to answer. Cite sources by their labels, such as [1].\n\n", result.Query); err != nil {
		return err
	}
	for _, block := range result.Blocks {
		if _, err := fmt.Fprintf(out, "%s\n%s\n\n", formatSource(block.Source), block.Text); err != nil {
			return err
		}
	}
	return nil
}

func promptContextString(result dogear.RetrievalResult) string {
	var builder strings.Builder
	_ = writePromptContext(&builder, result)
	return builder.String()
}
