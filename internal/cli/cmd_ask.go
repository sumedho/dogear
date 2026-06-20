package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	dogearadapter "github.com/sumedho/dogear/internal/adapters/dogear"
	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/retrievalpolicy"
)

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

			askOptions := app.AskOptions{
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
			}
			retriever := dogearadapter.NewConfiguredRetriever(store, resolveConfigPath(opts.configPath))
			if !dryRun && !jsonOut {
				result, err := app.AskStream(cmd.Context(), retriever, askOptions, func(delta string) error {
					_, err := fmt.Fprint(opts.out, delta)
					return err
				})
				if err != nil {
					return err
				}
				fmt.Fprintln(opts.out)
				fmt.Fprintln(opts.out)
				fmt.Fprintln(opts.out, "Sources:")
				for _, source := range result.Sources {
					fmt.Fprintf(opts.out, "%s\n", formatAppSource(source))
				}
				return nil
			}

			result, err := app.Ask(cmd.Context(), retriever, askOptions)
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
					Images:      result.Images,
				})
			}

			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", retrievalpolicy.DefaultContextLimit, "maximum context chunks")
	cmd.Flags().StringVar(&docID, "doc", "", "restrict retrieval to one document id")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print provider request without sending it")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "OpenAI-compatible base URL or chat completions URL")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "provider API key; optional for local endpoints")
	cmd.Flags().StringVar(&model, "model", "", "provider model name")
	cmd.Flags().StringVar(&timeoutValue, "timeout", "", "provider request timeout, such as 30s")
	return cmd
}
