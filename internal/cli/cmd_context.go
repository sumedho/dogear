package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	dogearadapter "github.com/sumedho/dogear/internal/adapters/dogear"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/retrievalpolicy"
	"strings"
)

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
			retriever := dogearadapter.NewConfiguredRetriever(store, resolveConfigPath(opts.configPath))
			result, err := retriever.RetrieveRaw(cmd.Context(), dogear.RetrieveOptions{
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
	cmd.Flags().IntVar(&limit, "limit", retrievalpolicy.DefaultContextLimit, "maximum context chunks")
	cmd.Flags().StringVar(&docID, "doc", "", "restrict context to one document id")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text, json, or prompt")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	cmd.Flags().BoolVar(&debug, "debug", false, "include retrieval scoring debug metadata")
	return cmd
}
