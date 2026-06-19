package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	dogearadapter "github.com/sumedho/dogear/internal/adapters/dogear"
	"github.com/sumedho/dogear/internal/dogear"
	"strings"
)

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
			retriever := dogearadapter.NewConfiguredRetriever(store, resolveConfigPath(opts.configPath))
			results, err := retriever.SearchRaw(cmd.Context(), dogear.SearchOptions{
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
