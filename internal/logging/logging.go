package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Level  string
	Format string
	File   string
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func New(config Config, fallback io.Writer) (*slog.Logger, io.Closer, error) {
	level, err := parseLevel(config.Level)
	if err != nil {
		return nil, nil, err
	}
	format := strings.ToLower(strings.TrimSpace(config.Format))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return nil, nil, fmt.Errorf("invalid log format %q; use text or json", config.Format)
	}

	writer := fallback
	var closer io.Closer = nopCloser{}
	if path := strings.TrimSpace(config.File); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return nil, nil, fmt.Errorf("set log file permissions: %w", err)
		}
		writer = file
		closer = file
	}

	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(writer, options)
	} else {
		handler = slog.NewTextHandler(writer, options)
	}
	return slog.New(handler), closer, nil
}

func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.Level(100)}))
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q; use debug, info, warn, or error", value)
	}
}
