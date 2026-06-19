package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/sumedho/dogear/internal/dogear"
)

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
