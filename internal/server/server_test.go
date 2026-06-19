package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sumedho/dogear/internal/dogear"
)

func TestAPIEndpoints(t *testing.T) {
	store := testStore(t)
	handler := New(Options{Store: store})

	tests := []struct {
		name string
		path string
	}{
		{name: "health", path: "/api/health"},
		{name: "documents", path: "/api/documents"},
		{name: "document", path: "/api/documents/test-synth"},
		{name: "search", path: "/api/search?q=local+control"},
		{name: "context", path: "/api/context?q=local+control"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
				t.Fatalf("content type = %q", contentType)
			}
		})
	}
}

func TestAskDryRun(t *testing.T) {
	store := testStore(t)
	handler := New(Options{Store: store})
	body := bytes.NewBufferString(`{"question":"local control","dry_run":true,"model":"test-model"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/ask", body)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		DryRun struct {
			Body struct {
				Model string `json:"model"`
			} `json:"body"`
		} `json:"dry_run"`
		Sources []struct {
			Label string `json:"label"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DryRun.Body.Model != "test-model" {
		t.Fatalf("model = %q", payload.DryRun.Body.Model)
	}
	if len(payload.Sources) == 0 {
		t.Fatal("expected sources")
	}
}

func testStore(t *testing.T) *dogear.Store {
	t.Helper()
	store, err := dogear.Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	})
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	err = store.UpsertDocument(context.Background(), dogear.Document{
		ID:         "test-synth",
		Title:      "Test Synth Manual",
		Brand:      "Test",
		Model:      "Synth",
		SourcePath: "test.md",
		SourceHash: "abc123",
		Tags:       []string{"manual"},
	}, []dogear.Chunk{
		{
			DocumentID:   "test-synth",
			Ordinal:      1,
			HeadingPath:  "MIDI > Local Control",
			HeadingLevel: 2,
			StartLine:    10,
			EndLine:      14,
			Text:         "Set Local Control to Off when using an external sequencer. This disconnects the keyboard from the internal tone generator.",
			TextHash:     "chunk1",
		},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RebuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}
