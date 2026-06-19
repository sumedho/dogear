package cli

import (
	"fmt"
	"github.com/spf13/cobra"
)

func newDoctorCommand(opts *rootOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check Dogear database health",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(opts)
			if err != nil {
				return err
			}
			defer store.Close()
			report, err := store.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(opts.out, doctorResponse{
					Database:      resolveDBPath(opts.dbPath),
					SchemaVersion: report.SchemaVersion,
					FTS5:          report.FTS5,
					Documents:     report.Documents,
					Chunks:        report.Chunks,
					IndexedChunks: report.IndexedChunks,
					OrphanChunks:  report.OrphanChunks,
				})
			}
			fmt.Fprintf(opts.out, "database: %s\n", resolveDBPath(opts.dbPath))
			fmt.Fprintf(opts.out, "schema version: %d\n", report.SchemaVersion)
			fmt.Fprintf(opts.out, "fts5: %t\n", report.FTS5)
			fmt.Fprintf(opts.out, "documents: %d\n", report.Documents)
			fmt.Fprintf(opts.out, "chunks: %d\n", report.Chunks)
			fmt.Fprintf(opts.out, "indexed chunks: %d\n", report.IndexedChunks)
			fmt.Fprintf(opts.out, "orphan chunks: %d\n", report.OrphanChunks)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "write JSON output")
	return cmd
}
