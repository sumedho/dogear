package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/server"
)

type rootOptions struct {
	dbPath     string
	configPath string
	out        io.Writer
	errOut     io.Writer
}

func NewRootCommand() *cobra.Command {
	return newRootCommand(os.Stdout, os.Stderr)
}

func newRootCommand(out, errOut io.Writer) *cobra.Command {
	opts := rootOptions{
		out:    out,
		errOut: errOut,
	}

	root := &cobra.Command{
		Use:           "dogear",
		Short:         "Search local Markdown manuals with SQLite FTS5",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.dbPath, "db", "", "SQLite database path (default .dogear/dogear.db)")
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "TOML config path (default .dogear/config.toml)")

	root.AddCommand(newInitCommand(&opts))
	root.AddCommand(newImportCommand(&opts))
	root.AddCommand(newIndexCommand(&opts))
	root.AddCommand(newListCommand(&opts))
	root.AddCommand(newInfoCommand(&opts))
	root.AddCommand(newRemoveCommand(&opts))
	root.AddCommand(newSearchCommand(&opts))
	root.AddCommand(newShowCommand(&opts))
	root.AddCommand(newContextCommand(&opts))
	root.AddCommand(newDoctorCommand(&opts))
	root.AddCommand(newAskCommand(&opts))
	root.AddCommand(newServeCommand(&opts))
	root.AddCommand(notImplementedCommand("convert", "Convert source documents to Markdown"))

	root.Flags().Bool("serve", false, "serve the local HTML UI")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		serve, _ := cmd.Flags().GetBool("serve")
		if serve {
			return runServe(cmd, &opts, "127.0.0.1:8765")
		}
		return cmd.Help()
	}

	return root
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func nullIntPtr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
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
	RawScore    float64  `json:"raw_score"`
	RerankScore float64  `json:"rerank_score"`
	Quality     string   `json:"quality"`
	Reasons     []string `json:"reasons"`
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
			PageNumber:  nullIntPtr(result.PageNumber),
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
			PageNumber:   nullIntPtr(chunk.PageNumber),
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
	}
}

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

func openStore(opts *rootOptions) (*dogear.Store, error) {
	return dogear.Open(resolveDBPath(opts.dbPath))
}

func resolveDBPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(".dogear", "dogear.db")
}

func resolveConfigPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(".dogear", "config.toml")
}

func notImplementedCommand(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("dogear %s is not implemented yet", name)
		},
	}
}

func newServeCommand(opts *rootOptions) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the local Dogear HTML UI and JSON API",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, opts, addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8765", "HTTP listen address")
	return cmd
}

func runServe(cmd *cobra.Command, opts *rootOptions, addr string) error {
	store, err := openStore(opts)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		return err
	}
	displayAddr := addr
	if host, port, err := net.SplitHostPort(addr); err == nil && (host == "" || host == "0.0.0.0") {
		displayAddr = net.JoinHostPort("127.0.0.1", port)
	}
	fmt.Fprintf(opts.out, "serving http://%s\n", displayAddr)
	return server.Serve(cmd.Context(), addr, store, resolveConfigPath(opts.configPath))
}

func newInitCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a Dogear database",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := resolveDBPath(opts.dbPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			store, err := dogear.Open(path)
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.Init(); err != nil {
				return err
			}
			configPath := resolveConfigPath(opts.configPath)
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				return err
			}
			if _, err := os.Stat(configPath); err != nil {
				if !os.IsNotExist(err) {
					return err
				}
				if err := os.WriteFile(configPath, []byte(defaultConfigTOML()), 0o644); err != nil {
					return err
				}
			}
			fmt.Fprintf(opts.out, "initialized %s\n", path)
			return nil
		},
	}
}

func defaultConfigTOML() string {
	return `[provider]
base_url = "http://localhost:11434/v1"
model = ""
api_key = ""
timeout = "60s"
`
}

func newImportCommand(opts *rootOptions) *cobra.Command {
	var meta dogear.ImportMetadata
	var replace bool

	cmd := &cobra.Command{
		Use:   "import PATH",
		Short: "Import Markdown manuals",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.Init(); err != nil {
				return err
			}

			result, err := dogear.ImportPath(cmd.Context(), store, args[0], meta, replace)
			if err != nil {
				return err
			}
			fmt.Fprintf(opts.out, "imported %d document(s), %d chunk(s)\n", result.Documents, result.Chunks)
			return nil
		},
	}

	cmd.Flags().StringVar(&meta.ID, "id", "", "document id for a single-file import")
	cmd.Flags().StringVar(&meta.Title, "title", "", "document title")
	cmd.Flags().StringVar(&meta.Brand, "brand", "", "document brand/manufacturer")
	cmd.Flags().StringVar(&meta.Model, "model", "", "document model")
	cmd.Flags().StringVar(&meta.Version, "version", "", "document/manual version")
	cmd.Flags().StringSliceVar(&meta.Tags, "tags", nil, "document tags")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace existing document ids")
	return cmd
}

func newIndexCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Rebuild the SQLite FTS5 index",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			count, err := store.RebuildIndex(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(opts.out, "indexed %d chunk(s)\n", count)
			return nil
		},
	}
}

func newListCommand(opts *rootOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List imported documents",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			documents, err := store.ListDocuments(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(opts.out, documentInfoResponses(documents))
			}
			for _, doc := range documents {
				fmt.Fprintf(opts.out, "%s | %s | %s %s | chunks %d | %s\n",
					doc.ID, doc.Title, doc.Brand, doc.Model, doc.ChunkCount, doc.SourcePath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	return cmd
}

func newInfoCommand(opts *rootOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "info DOC_ID",
		Short: "Show document metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			info, err := store.DocumentInfo(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(opts.out, documentInfoResponse(info))
			}
			fmt.Fprintf(opts.out, "id: %s\n", info.ID)
			fmt.Fprintf(opts.out, "title: %s\n", info.Title)
			fmt.Fprintf(opts.out, "brand: %s\n", info.Brand)
			fmt.Fprintf(opts.out, "model: %s\n", info.Model)
			fmt.Fprintf(opts.out, "version: %s\n", info.Version)
			fmt.Fprintf(opts.out, "tags: %s\n", strings.Join(info.Tags, ", "))
			fmt.Fprintf(opts.out, "source: %s\n", info.SourcePath)
			fmt.Fprintf(opts.out, "source hash: %s\n", info.SourceHash)
			fmt.Fprintf(opts.out, "chunks: %d\n", info.ChunkCount)
			fmt.Fprintf(opts.out, "indexed chunks: %d\n", info.IndexedChunks)
			fmt.Fprintf(opts.out, "pages: %d\n", info.PageCount)
			fmt.Fprintf(opts.out, "created: %s\n", info.CreatedAt)
			fmt.Fprintf(opts.out, "updated: %s\n", info.UpdatedAt)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	return cmd
}

func newRemoveCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "remove DOC_ID",
		Short: "Remove an imported document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.RemoveDocument(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(opts.out, "removed %s\n", args[0])
			return nil
		},
	}
}

func newSearchCommand(opts *rootOptions) *cobra.Command {
	var limit int
	var docID string
	var jsonOut bool
	var debug bool

	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search imported manuals",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			results, err := store.Search(cmd.Context(), dogear.SearchOptions{
				Query:      args[0],
				DocumentID: docID,
				Limit:      limit,
				Debug:      debug,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(opts.out, searchResultResponses(results, debug))
			}
			for i, result := range results {
				fmt.Fprintf(opts.out, "%s | score %.3f\n", formatSearchSource(result, i+1), result.Score)
				fmt.Fprintf(opts.out, "  %s\n\n", strings.ReplaceAll(result.Snippet, "\n", " "))
				if debug {
					fmt.Fprintf(opts.out, "  debug: raw %.3f rerank %.3f quality %s reasons %s\n\n",
						result.Debug.RawScore, result.Debug.RerankScore, result.Debug.Quality, strings.Join(result.Debug.Reasons, ","))
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum search results")
	cmd.Flags().StringVar(&docID, "doc", "", "restrict search to one document id")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	cmd.Flags().BoolVar(&debug, "debug", false, "include retrieval scoring debug metadata")
	return cmd
}

func newShowCommand(opts *rootOptions) *cobra.Command {
	var page int
	var section string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "show DOC_ID",
		Short: "Show stored manual content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			chunks, err := store.Show(cmd.Context(), dogear.ShowOptions{
				DocumentID: args[0],
				Page:       page,
				Section:    section,
			})
			if err != nil {
				return err
			}
			if len(chunks) == 0 {
				return fmt.Errorf("no content found for %q", args[0])
			}
			if jsonOut {
				return writeJSON(opts.out, chunkResponses(chunks))
			}
			for _, chunk := range chunks {
				fmt.Fprintf(opts.out, "## %s\n\n%s\n\n", chunk.HeadingPath, chunk.Text)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&page, "page", 0, "show chunks assigned to a page number")
	cmd.Flags().StringVar(&section, "section", "", "show chunks whose heading contains this text")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	return cmd
}

func newContextCommand(opts *rootOptions) *cobra.Command {
	var limit int
	var docID string
	var jsonOut bool
	var format string
	var debug bool

	cmd := &cobra.Command{
		Use:   "context QUESTION",
		Short: "Preview retrieval context for a future ask command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			result, err := store.Retrieve(cmd.Context(), dogear.RetrieveOptions{
				Query:      args[0],
				DocumentID: docID,
				Limit:      limit,
				Debug:      debug,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				format = "json"
			}
			switch format {
			case "json":
				return writeJSON(opts.out, retrievalResultResponse(result, debug))
			case "prompt":
				return writePromptContext(opts.out, result)
			case "text":
				for _, block := range result.Blocks {
					fmt.Fprintf(opts.out, "%s | score %.3f\n", formatSource(block.Source), block.Source.Score)
					if debug {
						fmt.Fprintf(opts.out, "debug: raw %.3f rerank %.3f quality %s reasons %s\n",
							block.Source.Debug.RawScore, block.Source.Debug.RerankScore, block.Source.Debug.Quality, strings.Join(block.Source.Debug.Reasons, ","))
					}
					fmt.Fprintf(opts.out, "%s\n\n", block.Text)
				}
				return nil
			default:
				return fmt.Errorf("unsupported context format %q; use text, json, or prompt", format)
			}
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 8, "maximum context chunks")
	cmd.Flags().StringVar(&docID, "doc", "", "restrict context to one document id")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text, json, or prompt")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	cmd.Flags().BoolVar(&debug, "debug", false, "include retrieval scoring debug metadata")
	return cmd
}

func newAskCommand(opts *rootOptions) *cobra.Command {
	var limit int
	var docID string
	var dryRun bool
	var jsonOut bool
	var baseURL string
	var apiKey string
	var model string
	var timeoutValue string

	cmd := &cobra.Command{
		Use:   "ask QUESTION",
		Short: "Answer a question using retrieved manual context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()

			result, err := app.Ask(cmd.Context(), store, app.AskOptions{
				Question:   args[0],
				DocumentID: docID,
				Limit:      limit,
				DryRun:     dryRun,
				ConfigPath: resolveConfigPath(opts.configPath),
				Provider: app.ProviderOverride{
					BaseURL: baseURL,
					APIKey:  apiKey,
					Model:   model,
					Timeout: timeoutValue,
				},
			})
			if err != nil {
				return err
			}

			if dryRun {
				return writeJSON(opts.out, result.DryRun)
			}

			if jsonOut {
				return writeJSON(opts.out, app.AskResponse{
					Answer:      result.Answer,
					Model:       result.Model,
					ProviderURL: result.ProviderURL,
					Sources:     result.Sources,
					Retrieval:   result.Retrieval,
				})
			}

			fmt.Fprintln(opts.out, result.Answer)
			fmt.Fprintln(opts.out)
			fmt.Fprintln(opts.out, "Sources:")
			for _, source := range result.Sources {
				fmt.Fprintf(opts.out, "%s\n", formatAppSource(source))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 8, "maximum context chunks")
	cmd.Flags().StringVar(&docID, "doc", "", "restrict retrieval to one document id")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print provider request without sending it")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "OpenAI-compatible base URL or chat completions URL")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "provider API key; optional for local endpoints")
	cmd.Flags().StringVar(&model, "model", "", "provider model name")
	cmd.Flags().StringVar(&timeoutValue, "timeout", "", "provider request timeout, such as 30s")
	return cmd
}

func newDoctorCommand(opts *rootOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check Dogear database health",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			report, err := store.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(opts.out, doctorResponse{
					Database:      resolveDBPath(opts.dbPath),
					SchemaVersion: report.SchemaVersion,
					FTS5:          report.FTS5,
					Documents:     report.Documents,
					Chunks:        report.Chunks,
					IndexedChunks: report.IndexedChunks,
					OrphanChunks:  report.OrphanChunks,
				})
			}
			fmt.Fprintf(opts.out, "database: %s\n", resolveDBPath(opts.dbPath))
			fmt.Fprintf(opts.out, "schema version: %d\n", report.SchemaVersion)
			fmt.Fprintf(opts.out, "fts5: %t\n", report.FTS5)
			fmt.Fprintf(opts.out, "documents: %d\n", report.Documents)
			fmt.Fprintf(opts.out, "chunks: %d\n", report.Chunks)
			fmt.Fprintf(opts.out, "indexed chunks: %d\n", report.IndexedChunks)
			fmt.Fprintf(opts.out, "orphan chunks: %d\n", report.OrphanChunks)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	return cmd
}
