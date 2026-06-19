package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatCompletionsURL(t *testing.T) {
	tests := map[string]string{
		"http://localhost:11434":                     "http://localhost:11434/v1/chat/completions",
		"http://localhost:11434/v1":                  "http://localhost:11434/v1/chat/completions",
		"http://localhost:11434/v1/chat/completions": "http://localhost:11434/v1/chat/completions",
	}
	for input, want := range tests {
		got, err := ChatCompletionsURL(input)
		if err != nil {
			t.Fatalf("ChatCompletionsURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ChatCompletionsURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMergeConfig(t *testing.T) {
	file := Config{
		BaseURL: "http://file/v1",
		APIKey:  "file-key",
		Model:   "file-model",
		Timeout: 2 * time.Second,
	}
	env := Config{
		BaseURL: "http://env/v1",
		APIKey:  "env-key",
		Model:   "env-model",
		Timeout: time.Second,
	}
	flags := Config{
		BaseURL: "http://flag/v1",
		Model:   "flag-model",
	}
	got := MergeConfig(MergeConfig(file, env), flags)
	if got.BaseURL != "http://flag/v1" || got.APIKey != "env-key" || got.Model != "flag-model" || got.Timeout != time.Second {
		t.Fatalf("unexpected merged config: %#v", got)
	}
}

func TestClientChatWithOptionalAuth(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		var request ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" {
			t.Fatalf("model = %q", request.Model)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Use MIDI config [1]."}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Chat(context.Background(), BuildRequest("test-model", []Message{{Role: "user", Content: "question"}}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Use MIDI config [1]." {
		t.Fatalf("content = %q", response.Content)
	}
	if auth != "" {
		t.Fatalf("auth header = %q, want empty", auth)
	}

	client, err = NewClient(Config{BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Chat(context.Background(), BuildRequest("test-model", []Message{{Role: "user", Content: "question"}})); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" {
		t.Fatalf("auth header = %q, want bearer", auth)
	}
}

func TestClientChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.Stream {
			t.Fatal("stream = false, want true")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Use \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"MIDI [1].\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	response, err := client.ChatStream(context.Background(), BuildRequest("test-model", []Message{{Role: "user", Content: "question"}}), func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Use MIDI [1]." || strings.Join(deltas, "") != response.Content {
		t.Fatalf("response = %#v, deltas = %#v", response, deltas)
	}
}

func TestClientChatStreamRejectsMalformedEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\n\n")
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ChatStream(context.Background(), BuildRequest("test-model", nil), nil); err == nil {
		t.Fatal("ChatStream() error = nil, want malformed stream error")
	}
}

func TestClientProviderErrors(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
	}{
		"http error":    {status: http.StatusBadRequest, body: `bad request`},
		"malformed":     {status: http.StatusOK, body: `{`},
		"empty choices": {status: http.StatusOK, body: `{"choices":[]}`},
		"empty content": {status: http.StatusOK, body: `{"choices":[{"message":{"content":""}}]}`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client, err := NewClient(Config{BaseURL: server.URL + "/v1", Model: "test-model"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Chat(context.Background(), BuildRequest("test-model", []Message{{Role: "user", Content: "question"}})); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("DOGEAR_BASE_URL", "http://env/v1")
	t.Setenv("DOGEAR_API_KEY", "env-key")
	t.Setenv("DOGEAR_MODEL", "env-model")
	t.Setenv("DOGEAR_TIMEOUT", "5s")

	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.BaseURL != "http://env/v1" || config.APIKey != "env-key" || config.Model != "env-model" || config.Timeout != 5*time.Second {
		t.Fatalf("unexpected env config: %#v", config)
	}
}

func TestParseConfigTOML(t *testing.T) {
	config, err := ParseConfigTOML(`
# top-level provider keys are accepted
base_url = "http://ignored.example/v1"

[provider]
base_url = "http://localhost:11434/v1" # local endpoint
api_key = "secret#kept"
model = "llama3.1"
timeout = "15s"

[other]
model = "ignored"
`)
	if err != nil {
		t.Fatal(err)
	}
	if config.BaseURL != "http://localhost:11434/v1" || config.APIKey != "secret#kept" || config.Model != "llama3.1" || config.Timeout != 15*time.Second {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParseConfigTOMLFeatures(t *testing.T) {
	config, err := ParseConfigTOML(`
model = "top-level"
api_key = "top-level-key"

[provider]
model = """multi-line
model"""
api_key = '''literal#key'''
timeout = "1m30s"
features = ["chat", "embeddings"]

[future.nested]
enabled = true
`)
	if err != nil {
		t.Fatal(err)
	}
	if config.Model != "multi-line\nmodel" || config.APIKey != "literal#key" || config.Timeout != 90*time.Second {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParseConfigTOMLProviderEmptyValueOverridesTopLevel(t *testing.T) {
	config, err := ParseConfigTOML(`
api_key = "top-level-key"

[provider]
api_key = ""
`)
	if err != nil {
		t.Fatal(err)
	}
	if config.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", config.APIKey)
	}
}

func TestParseConfigTOMLErrors(t *testing.T) {
	tests := map[string]string{
		"malformed":        `model = "unterminated`,
		"wrong field type": `model = ["one", "two"]`,
		"invalid timeout":  `timeout = "eventually"`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfigTOML(content); err == nil {
				t.Fatal("ParseConfigTOML() error = nil, want error")
			}
		})
	}
}

func TestConfigFromTOMLFile(t *testing.T) {
	dir := t.TempDir()
	missing, err := ConfigFromTOMLFile(filepath.Join(dir, "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if missing != (Config{}) {
		t.Fatalf("missing config = %#v, want zero", missing)
	}

	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`[provider]
model = "qwen2.5"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := ConfigFromTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Model != "qwen2.5" {
		t.Fatalf("model = %q", config.Model)
	}
}
