package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/embedding"
)

func newIndexCommand(opts *rootOptions) *cobra.Command {
	var embeddings bool
	var force bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Rebuild the SQLite FTS5 index",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.InitContext(cmd.Context()); err != nil {
				return err
			}
			count, err := store.RebuildIndex(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(opts.out, "indexed %d chunk(s)\n", count)
			if embeddings {
				provider, err := app.ProviderConfig(resolveConfigPath(opts.configPath), app.ProviderOverride{})
				if err != nil {
					return err
				}
				config, err := embedding.Resolve(resolveConfigPath(opts.configPath), provider.BaseURL, provider.APIKey)
				if err != nil {
					return err
				}
				client, err := embedding.NewClient(config)
				if err != nil {
					return err
				}
				status, err := store.BuildEmbeddingIndex(cmd.Context(), config.Model, config.Dimensions, config.BatchSize, config.IndexHash(), force, client.Embed, func(indexed, total int) {
					fmt.Fprintf(opts.errOut, "embedding %d/%d chunks\r", indexed, total)
				})
				fmt.Fprintln(opts.errOut)
				if err != nil {
					return err
				}
				fmt.Fprintf(opts.out, "embedded %d chunk(s) with %s (%d dimensions)\n", status.Indexed, status.Model, status.Dimensions)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&embeddings, "embeddings", false, "build the configured vector index")
	cmd.Flags().BoolVar(&force, "force", false, "rebuild embeddings even when the index is current")
	return cmd
}
