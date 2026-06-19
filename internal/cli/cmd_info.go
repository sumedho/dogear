package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"strings"
)

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
