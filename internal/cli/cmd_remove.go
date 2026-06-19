package cli

import (
	"fmt"
	"github.com/spf13/cobra"
)

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
