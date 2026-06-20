package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/sumedho/dogear/internal/dogear"
	"os"
	"path/filepath"
)

func newInitCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a DogEar database",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := resolveDBPath(opts.dbPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			store, err := dogear.Open(path)
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.Init(); err != nil {
				return err
			}
			configPath := resolveConfigPath(opts.configPath)
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				return err
			}
			if _, err := os.Stat(configPath); err != nil {
				if !os.IsNotExist(err) {
					return err
				}
				if err := os.WriteFile(configPath, []byte(defaultConfigTOML()), 0o600); err != nil {
					return err
				}
			}
			fmt.Fprintf(opts.out, "initialized %s\n", path)
			return nil
		},
	}
}

func defaultConfigTOML() string {
	return `[provider]
base_url = "http://localhost:11434/v1"
model = ""
api_key = ""
timeout = "60s"

[embedding]
base_url = "http://localhost:8000/v1"
model = ""
api_key = ""
dimensions = 1024
batch_size = 16
query_instruction = "Retrieve relevant passages from product manuals that answer the user's question."
timeout = "120s"
`
}
