package cli

import (
	"fmt"
	"github.com/spf13/cobra"
)

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
