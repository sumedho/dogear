package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sumedho/dogear/internal/dogear"
)

const serverTestPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

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
		{name: "document health", path: "/api/documents/test-synth/health"},
		{name: "search", path: "/api/search?q=local+control"},
		{name: "context", path: "/api/context?q=local+control"},
		{name: "document chunks", path: "/api/documents/test-synth/chunks"},
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

func TestRemoveDocument(t *testing.T) {
	handler := New(Options{Store: testStore(t)})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/documents/test-synth", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/documents/test-synth", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("removed document status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestSettingsAPIHidesKeys(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := `[provider]
base_url = "http://localhost:8000/v1"
model = "chat"
api_key = "top-secret"
timeout = "60s"
[embedding]
base_url = "http://localhost:8000/v1"
model = "embed"
api_key = "embedding-secret"
dimensions = 1024
batch_size = 16
timeout = "120s"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Store: testStore(t), ConfigPath: configPath})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "top-secret") || strings.Contains(response.Body.String(), "embedding-secret") {
		t.Fatalf("unsafe settings response: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"api_key_set":true`) {
		t.Fatalf("missing masked key state: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"environment_overrides":null`) {
		t.Fatalf("environment overrides must be an array: %s", response.Body.String())
	}
	var payload settingsPayload
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Embedding.Model != "embed" || payload.Embedding.Dimensions != 1024 {
		t.Fatalf("unexpected settings: %#v", payload)
	}
	payload.Provider.Model = "updated-chat"
	payload.Provider.APIKeyAction = "preserve"
	payload.Embedding.APIKeyAction = "preserve"
	raw, _ := json.Marshal(payload)
	update := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(update, request)
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}
	saved, _ := os.ReadFile(configPath)
	if !strings.Contains(string(saved), `model = "updated-chat"`) || !strings.Contains(string(saved), `api_key = "top-secret"`) {
		t.Fatalf("settings update lost values: %s", saved)
	}
}

func TestSettingsTestUsesDraftWithoutSaving(t *testing.T) {
	var authorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			t.Fatalf("draft test path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer provider.Close()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := `[provider]
base_url = "http://saved.invalid/v1"
model = "saved-model"
api_key = "saved-key"
timeout = "60s"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Store: testStore(t), ConfigPath: configPath})
	payload := map[string]any{
		"target":   "provider",
		"provider": map[string]any{"base_url": provider.URL + "/v1", "model": "draft-model", "timeout": "5s", "api_key_action": "replace", "api_key": "draft-key"},
	}
	raw, _ := json.Marshal(payload)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/settings/test", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model":"draft-model"`) {
		t.Fatalf("draft test status=%d body=%s", response.Code, response.Body.String())
	}
	if authorization != "Bearer draft-key" {
		t.Fatalf("authorization = %q", authorization)
	}
	saved, _ := os.ReadFile(configPath)
	if !strings.Contains(string(saved), "saved-model") || strings.Contains(string(saved), "draft-model") || strings.Contains(string(saved), "draft-key") {
		t.Fatalf("draft test mutated settings: %s", saved)
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

func TestContextDebugResponse(t *testing.T) {
	store := testStore(t)
	handler := New(Options{Store: store})
	request := httptest.NewRequest(http.MethodGet, "/api/context?q=local+control&debug=true", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Query  string `json:"query"`
		Blocks []struct {
			Source struct {
				DocumentID string `json:"document_id"`
				Debug      *struct {
					Quality string `json:"quality"`
				} `json:"debug"`
			} `json:"source"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Query != "local control" || len(payload.Blocks) == 0 {
		t.Fatalf("unexpected context response: %#v", payload)
	}
	if payload.Blocks[0].Source.DocumentID != "test-synth" || payload.Blocks[0].Source.Debug == nil {
		t.Fatalf("unexpected source response: %#v", payload.Blocks[0].Source)
	}
}

func TestDocumentAndSearchResponseContracts(t *testing.T) {
	store := testStore(t)
	handler := New(Options{Store: store})

	documentsRequest := httptest.NewRequest(http.MethodGet, "/api/documents", nil)
	documentsResponse := httptest.NewRecorder()
	handler.ServeHTTP(documentsResponse, documentsRequest)
	var documents []struct {
		ID         string   `json:"id"`
		SourcePath string   `json:"source_path"`
		Tags       []string `json:"tags"`
	}
	if err := json.Unmarshal(documentsResponse.Body.Bytes(), &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].ID != "test-synth" || documents[0].SourcePath != "test.md" || len(documents[0].Tags) != 1 {
		t.Fatalf("unexpected documents response: %#v", documents)
	}

	searchRequest := httptest.NewRequest(http.MethodGet, "/api/search?q=local+control", nil)
	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, searchRequest)
	var results []struct {
		DocumentID string          `json:"document_id"`
		Snippet    string          `json:"snippet"`
		Debug      json.RawMessage `json:"debug"`
	}
	if err := json.Unmarshal(searchResponse.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].DocumentID != "test-synth" || results[0].Snippet == "" || results[0].Debug != nil {
		t.Fatalf("unexpected search response: %#v", results)
	}
}

func TestImportAndServeEmbeddedImage(t *testing.T) {
	store := testStore(t)
	handler := New(Options{Store: store})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "images.md")
	if err != nil {
		t.Fatal(err)
	}
	markdown := "# Images\n\n## Diagram\n\nThis section contains the relevant illustration.\n\n![Signal flow schematic](data:image/png;base64," + serverTestPNGBase64 + ")\n"
	if _, err := part.Write([]byte(markdown)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	searchRequest := httptest.NewRequest(http.MethodGet, "/api/search?q=signal+flow+schematic&doc=images", nil)
	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, searchRequest)
	var searchResults []searchResultResponse
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", searchResponse.Code, searchResponse.Body.String())
	}
	if err := json.Unmarshal(searchResponse.Body.Bytes(), &searchResults); err != nil {
		t.Fatal(err)
	}
	if len(searchResults) != 1 || len(searchResults[0].Images) != 1 || searchResults[0].Images[0].Alt != "Signal flow schematic" {
		t.Fatalf("unexpected image search response: %#v", searchResults)
	}
	if searchResults[0].ChunkID <= 0 {
		t.Fatalf("search result does not include a deep-linkable chunk id: %#v", searchResults[0])
	}
	contextRequest := httptest.NewRequest(http.MethodGet, "/api/context?q=signal+flow+schematic&doc=images", nil)
	contextResponse := httptest.NewRecorder()
	handler.ServeHTTP(contextResponse, contextRequest)
	var retrieval retrievalResultResponse
	if contextResponse.Code != http.StatusOK {
		t.Fatalf("context status = %d, body = %s", contextResponse.Code, contextResponse.Body.String())
	}
	if err := json.Unmarshal(contextResponse.Body.Bytes(), &retrieval); err != nil {
		t.Fatal(err)
	}
	if len(retrieval.Blocks) != 1 || len(retrieval.Blocks[0].Images) != 1 {
		t.Fatalf("unexpected context response: %#v", retrieval)
	}
	askRequest := httptest.NewRequest(http.MethodPost, "/api/ask", strings.NewReader(`{"question":"display the signal flow schematic","doc":"images","dry_run":true,"model":"test-model"}`))
	askResponseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(askResponseRecorder, askRequest)
	var answer askResponse
	if askResponseRecorder.Code != http.StatusOK {
		t.Fatalf("ask status = %d, body = %s", askResponseRecorder.Code, askResponseRecorder.Body.String())
	}
	if err := json.Unmarshal(askResponseRecorder.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Images) != 1 || answer.Images[0].Alt != "Signal flow schematic" || answer.Images[0].Source.DocumentID != "images" {
		t.Fatalf("unexpected answer images: %#v", answer.Images)
	}
	imageRequest := httptest.NewRequest(http.MethodGet, "/api/images/"+strconv.FormatInt(retrieval.Blocks[0].Images[0].ID, 10), nil)
	imageResponse := httptest.NewRecorder()
	handler.ServeHTTP(imageResponse, imageRequest)
	want, _ := base64.StdEncoding.DecodeString(serverTestPNGBase64)
	if imageResponse.Code != http.StatusOK || imageResponse.Header().Get("Content-Type") != "image/png" || !bytes.Equal(imageResponse.Body.Bytes(), want) {
		t.Fatalf("unexpected image response: status=%d headers=%v", imageResponse.Code, imageResponse.Header())
	}
	cachedRequest := httptest.NewRequest(http.MethodGet, imageRequest.URL.Path, nil)
	cachedRequest.Header.Set("If-None-Match", imageResponse.Header().Get("ETag"))
	cachedResponse := httptest.NewRecorder()
	handler.ServeHTTP(cachedResponse, cachedRequest)
	if cachedResponse.Code != http.StatusNotModified || cachedResponse.Header().Get("ETag") == "" {
		t.Fatalf("unexpected cached image response: status=%d headers=%v", cachedResponse.Code, cachedResponse.Header())
	}
}

func TestAskStreamSSE(t *testing.T) {
	store := testStore(t)
	var messageCount int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream   bool  `json:"stream"`
			Messages []any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.Stream {
			t.Fatal("provider request did not enable streaming")
		}
		messageCount = len(request.Messages)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Turn it \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"off [1].\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer provider.Close()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[provider]\nbase_url = \""+provider.URL+"/v1\"\nmodel = \"test-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Store: store, ConfigPath: configPath})
	body := bytes.NewBufferString(`{"question":"local control","history":[{"role":"user","content":"Earlier"},{"role":"assistant","content":"Earlier answer"}]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/ask/stream", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if messageCount != 4 || !strings.Contains(response.Body.String(), "event: delta") || !strings.Contains(response.Body.String(), "event: result") || !strings.Contains(response.Body.String(), "Turn it off [1].") {
		t.Fatalf("messageCount=%d stream=%s", messageCount, response.Body.String())
	}
}

func TestEmbeddedSPA(t *testing.T) {
	handler := New(Options{Store: testStore(t)})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestStructuredAccessLogs(t *testing.T) {
	store, err := dogear.Open(filepath.Join(t.TempDir(), "dogear.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handler := New(Options{Store: store, Logger: logger})

	for _, path := range []string{"/api/health", "/api/search?unused=secret+terms"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/documents", nil))

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("log records = %d, want 3: %s", len(lines), output.String())
	}
	wantLevels := []string{"INFO", "WARN", "ERROR"}
	wantStatuses := []float64{200, 400, 500}
	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] != "http request" || record["level"] != wantLevels[i] || record["status"] != wantStatuses[i] {
			t.Fatalf("record %d = %#v", i, record)
		}
		if strings.Contains(record["path"].(string), "secret") || strings.Contains(line, "secret terms") {
			t.Fatalf("query leaked into log: %s", line)
		}
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
