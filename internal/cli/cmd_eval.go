package cli

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	dogearadapter "github.com/sumedho/dogear/internal/adapters/dogear"
	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/embedding"
	"github.com/sumedho/dogear/internal/evaluation"
)

func newEvalCommand(opts *rootOptions) *cobra.Command {
	var mode string
	var ks []int
	var answers, jsonOut bool
	var minRecall, minMRR float64
	cmd := &cobra.Command{Use: "eval FIXTURE", Short: "Evaluate retrieval and citation quality", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		fixture, err := evaluation.Load(args[0])
		if err != nil {
			return err
		}
		if mode != "fts" && mode != "hybrid" && mode != "both" {
			return fmt.Errorf("mode must be fts, hybrid, or both")
		}
		store, err := openStore(opts)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.InitContext(cmd.Context()); err != nil {
			return err
		}
		configPath := resolveConfigPath(opts.configPath)
		provider, err := app.ProviderConfig(configPath, app.ProviderOverride{})
		if err != nil {
			return err
		}
		embedConfig, err := embedding.Resolve(configPath, provider.BaseURL, provider.APIKey)
		if err != nil {
			return err
		}
		embedClient, _ := embedding.NewClient(embedConfig)
		retrieve := func(ctx context.Context, selected string, item evaluation.Case, limit int) (dogear.RetrievalResult, error) {
			retrieveOpts := dogear.RetrieveOptions{Query: item.Query, DocumentID: item.DocumentID, Limit: limit}
			if selected == "hybrid" {
				if embedClient == nil {
					return dogear.RetrievalResult{}, fmt.Errorf("embedding model is not configured")
				}
				status, err := store.EmbeddingStatus(ctx, embedConfig.Model, embedConfig.Dimensions, embedConfig.IndexHash())
				if err != nil {
					return dogear.RetrievalResult{}, err
				}
				if !status.Complete {
					return dogear.RetrievalResult{}, fmt.Errorf("embedding index is stale; run dogear index --embeddings")
				}
				vector, err := embedClient.EmbedQuery(ctx, item.Query)
				if err != nil {
					return dogear.RetrievalResult{}, err
				}
				return store.RetrieveHybrid(ctx, retrieveOpts, vector)
			}
			return store.Retrieve(ctx, retrieveOpts)
		}
		var answer evaluation.AnswerFunc
		if answers {
			answer = func(ctx context.Context, selected string, item evaluation.Case) (string, error) {
				var retriever app.Retriever
				if selected == "hybrid" {
					value := dogearadapter.NewConfiguredRetriever(store, configPath)
					retriever = value
				} else {
					value := dogearadapter.NewRetriever(store)
					retriever = value
				}
				result, err := app.Ask(ctx, retriever, app.AskOptions{Question: item.Query, DocumentID: item.DocumentID, ConfigPath: configPath})
				return result.Answer, err
			}
		}
		modes := []string{mode}
		if mode == "both" {
			modes = []string{"fts", "hybrid"}
		}
		reports := make([]evaluation.Report, 0, len(modes))
		failed := false
		for _, selected := range modes {
			report := evaluation.Run(cmd.Context(), fixture, selected, ks, retrieve, answer)
			reports = append(reports, report)
			if report.MRR < minMRR || report.RecallAt[5] < minRecall {
				failed = true
			}
		}
		if jsonOut {
			if err := writeJSON(opts.out, reports); err != nil {
				return err
			}
		} else {
			for _, report := range reports {
				fmt.Fprintf(opts.out, "%s: MRR %.3f Recall@5 %.3f nDCG@5 %.3f latency %.1fms\n", report.Mode, report.MRR, report.RecallAt[5], report.NDCGAt[5], report.AverageLatencyMS)
			}
		}
		if failed {
			return fmt.Errorf("evaluation thresholds not met")
		}
		return nil
	}}
	cmd.Flags().StringVar(&mode, "mode", "both", "retrieval mode: fts, hybrid, both")
	cmd.Flags().IntSliceVar(&ks, "k", []int{1, 3, 5}, "ranking cutoffs")
	cmd.Flags().BoolVar(&answers, "answers", false, "also evaluate answer terms and citation validity")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	cmd.Flags().Float64Var(&minRecall, "min-recall-at-5", 0, "minimum Recall@5 threshold")
	cmd.Flags().Float64Var(&minMRR, "min-mrr", 0, "minimum MRR threshold")
	return cmd
}
