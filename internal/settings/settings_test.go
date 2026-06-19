package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sumedho/dogear/internal/embedding"
	"github.com/sumedho/dogear/internal/llm"
)

func TestWritePreservesUnknownSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[future]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	values := Values{Provider: llm.Config{BaseURL: "http://chat/v1", APIKey: "secret", Model: "chat", Timeout: time.Minute}, Embedding: embedding.Config{BaseURL: "http://embed/v1", Model: "embed", Dimensions: 1024, BatchSize: 16, QueryInstruction: "retrieve", Timeout: 2 * time.Minute}}
	if err := Write(path, values); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[future]") || !strings.Contains(string(raw), "[embedding]") {
		t.Fatalf("unexpected config: %s", raw)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}
