package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/sumedho/dogear/internal/dogear"
)

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
