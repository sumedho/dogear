package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientEmbedsBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var request struct {
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		data := make([]map[string]any, len(request.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": make([]float32, request.Dimensions)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL + "/v1", APIKey: "secret", Model: "embed", Dimensions: 32})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := client.Embed(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 32 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
}

func TestResolveOnlyInheritsKeyForSameOrigin(t *testing.T) {
	config := Config{BaseURL: "http://localhost:8000/v1", Model: "embed", Dimensions: 32, BatchSize: 1}
	if !sameOrigin(config.BaseURL, "http://localhost:8000/v1") || sameOrigin(config.BaseURL, "http://other:8000/v1") {
		t.Fatal("sameOrigin returned unexpected result")
	}
}
