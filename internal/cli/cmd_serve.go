package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/sumedho/dogear/internal/server"
	"net"
)

func newServeCommand(opts *rootOptions) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the local DogEar HTML UI and JSON API",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, opts, addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8765", "HTTP listen address")
	return cmd
}

func runServe(cmd *cobra.Command, opts *rootOptions, addr string) error {
	store, err := openStore(opts)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Init(); err != nil {
		return err
	}
	displayAddr := addr
	if host, port, err := net.SplitHostPort(addr); err == nil && (host == "" || host == "0.0.0.0") {
		displayAddr = net.JoinHostPort("127.0.0.1", port)
	}
	fmt.Fprintf(opts.out, "serving http://%s\n", displayAddr)
	return server.Serve(cmd.Context(), addr, store, resolveConfigPath(opts.configPath), opts.logger)
}
