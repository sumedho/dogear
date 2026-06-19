package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewTextLoggerFiltersLevels(t *testing.T) {
	var output bytes.Buffer
	logger, closer, err := New(Config{Level: "warn", Format: "text"}, &output)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	logger.Info("hidden")
	logger.Warn("visible", "count", 2)
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), "level=WARN") || !strings.Contains(output.String(), "count=2") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestNewJSONFileLoggerAppendsSecurely(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dogear.log")
	var fallback bytes.Buffer
	for _, message := range []string{"first", "second"} {
		logger, closer, err := New(Config{Level: "debug", Format: "json", File: path}, &fallback)
		if err != nil {
			t.Fatal(err)
		}
		logger.Debug(message, "document_id", "manual")
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if fallback.Len() != 0 {
		t.Fatalf("file logging also wrote to fallback: %s", fallback.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want 600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2: %s", len(lines), raw)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &record); err != nil {
		t.Fatal(err)
	}
	if record["msg"] != "second" || record["level"] != "DEBUG" || record["document_id"] != "manual" {
		t.Fatalf("unexpected record: %#v", record)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	for name, config := range map[string]Config{
		"level":  {Level: "trace"},
		"format": {Format: "yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := New(config, &bytes.Buffer{}); err == nil {
				t.Fatal("New() error = nil, want error")
			}
		})
	}
}

func TestDiscard(t *testing.T) {
	if Discard().Enabled(t.Context(), slog.LevelInfo) {
		t.Fatal("discard logger unexpectedly enabled info")
	}
}
