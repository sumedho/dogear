package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/logging"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type rootOptions struct {
	dbPath     string
	configPath string
	out        io.Writer
	errOut     io.Writer
	logLevel   string
	logFormat  string
	logFile    string
	logger     *slog.Logger
	logCloser  io.Closer
}

func NewRootCommand() *cobra.Command {
	command, _ := newRootCommandWithOptions(os.Stdout, os.Stderr)
	return command
}

func newRootCommand(out, errOut io.Writer) *cobra.Command {
	command, _ := newRootCommandWithOptions(out, errOut)
	return command
}

func Execute() error {
	command, opts := newRootCommandWithOptions(os.Stdout, os.Stderr)
	defer opts.closeLogger()
	return command.Execute()
}

func newRootCommandWithOptions(out, errOut io.Writer) (*cobra.Command, *rootOptions) {
	opts := rootOptions{
		out:       out,
		errOut:    errOut,
		logLevel:  "info",
		logFormat: "text",
		logger:    logging.Discard(),
	}

	root := &cobra.Command{
		Use:           "dogear",
		Short:         "Search local Markdown manuals with SQLite FTS5",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.dbPath, "db", "", "SQLite database path (default .dogear/dogear.db)")
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "TOML config path (default .dogear/config.toml)")
	root.PersistentFlags().StringVar(&opts.logLevel, "log-level", "info", "diagnostic log level: debug, info, warn, error")
	root.PersistentFlags().StringVar(&opts.logFormat, "log-format", "text", "diagnostic log format: text or json")
	root.PersistentFlags().StringVar(&opts.logFile, "log-file", "", "append diagnostic logs to this file instead of stderr")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		logger, closer, err := logging.New(logging.Config{Level: opts.logLevel, Format: opts.logFormat, File: opts.logFile}, opts.errOut)
		if err != nil {
			return err
		}
		opts.logger = logger
		opts.logCloser = closer
		return nil
	}
	root.PersistentPostRun = func(cmd *cobra.Command, args []string) {
		opts.closeLogger()
	}

	root.AddCommand(newInitCommand(&opts))
	root.AddCommand(newImportCommand(&opts))
	root.AddCommand(newIndexCommand(&opts))
	root.AddCommand(newListCommand(&opts))
	root.AddCommand(newInfoCommand(&opts))
	root.AddCommand(newRemoveCommand(&opts))
	root.AddCommand(newSearchCommand(&opts))
	root.AddCommand(newShowCommand(&opts))
	root.AddCommand(newContextCommand(&opts))
	root.AddCommand(newDoctorCommand(&opts))
	root.AddCommand(newEvalCommand(&opts))
	root.AddCommand(newAskCommand(&opts))
	root.AddCommand(newServeCommand(&opts))
	root.AddCommand(notImplementedCommand("convert", "Convert source documents to Markdown"))

	root.Flags().Bool("serve", false, "serve the local HTML UI")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		serve, _ := cmd.Flags().GetBool("serve")
		if serve {
			return runServe(cmd, &opts, "127.0.0.1:8765")
		}
		return cmd.Help()
	}

	return root, &opts
}

func (opts *rootOptions) closeLogger() {
	if opts.logCloser != nil {
		_ = opts.logCloser.Close()
		opts.logCloser = nil
	}
}

func openStore(opts *rootOptions) (*dogear.Store, error) {
	return dogear.Open(resolveDBPath(opts.dbPath))
}

func openServerStore(opts *rootOptions) (*dogear.Store, error) {
	return dogear.OpenWithOptions(resolveDBPath(opts.dbPath), dogear.StoreOptions{MaxOpenConns: 4})
}

func resolveDBPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(".dogear", "dogear.db")
}

func resolveConfigPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(".dogear", "config.toml")
}

func notImplementedCommand(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("dogear %s is not implemented yet", name)
		},
	}
}
