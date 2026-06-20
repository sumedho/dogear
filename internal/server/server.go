package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dogearadapter "github.com/sumedho/dogear/internal/adapters/dogear"
	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/embedding"
	"github.com/sumedho/dogear/internal/llm"
	"github.com/sumedho/dogear/internal/logging"
	"github.com/sumedho/dogear/internal/settings"
)

//go:embed static/*
var staticFiles embed.FS

type Options struct {
	Store      *dogear.Store
	ConfigPath string
	Logger     *slog.Logger
}

type Handler struct {
	store        *dogear.Store
	retriever    app.Retriever
	rawRetriever dogearadapter.Retriever
	configPath   string
	logger       *slog.Logger
	mux          *http.ServeMux
}

type askRequest struct {
	Question string                    `json:"question"`
	Document string                    `json:"doc"`
	Limit    int                       `json:"limit"`
	DryRun   bool                      `json:"dry_run"`
	BaseURL  string                    `json:"base_url"`
	APIKey   string                    `json:"api_key"`
	Model    string                    `json:"model"`
	Timeout  string                    `json:"timeout"`
	History  []app.ConversationMessage `json:"history"`
}

type askResponse struct {
	Answer      string              `json:"answer,omitempty"`
	Model       string              `json:"model"`
	ProviderURL string              `json:"provider_url"`
	Sources     []app.SourceRef     `json:"sources"`
	Retrieval   app.RetrievalResult `json:"retrieval"`
	Images      []app.DisplayImage  `json:"images,omitempty"`
	DryRun      *llm.DryRun         `json:"dry_run,omitempty"`
}

type healthResponse struct {
	OK bool `json:"ok"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func New(options Options) http.Handler {
	logger := options.Logger
	if logger == nil {
		logger = logging.Discard()
	}
	rawRetriever := dogearadapter.NewConfiguredRetriever(options.Store, options.ConfigPath)
	handler := &Handler{
		store:        options.Store,
		retriever:    rawRetriever,
		rawRetriever: rawRetriever,
		configPath:   options.ConfigPath,
		logger:       logger,
		mux:          http.NewServeMux(),
	}
	handler.routes()
	return handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	response := &loggingResponseWriter{ResponseWriter: w}
	h.mux.ServeHTTP(response, r)
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	level := slog.LevelInfo
	if status >= 500 {
		level = slog.LevelError
	} else if status >= 400 {
		level = slog.LevelWarn
	}
	h.logger.LogAttrs(r.Context(), level, "http request",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Int("bytes", response.bytes),
		slog.Int64("duration_ms", time.Since(started).Milliseconds()),
	)
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (h *Handler) routes() {
	h.mux.HandleFunc("GET /api/health", h.health)
	h.mux.HandleFunc("GET /api/documents", h.documents)
	h.mux.HandleFunc("GET /api/documents/{id}", h.document)
	h.mux.HandleFunc("DELETE /api/documents/{id}", h.removeDocument)
	h.mux.HandleFunc("GET /api/documents/{id}/health", h.documentHealth)
	h.mux.HandleFunc("GET /api/documents/{id}/chunks", h.documentChunks)
	h.mux.HandleFunc("GET /api/documents/{id}/chunks/{chunkID}", h.documentChunk)
	h.mux.HandleFunc("GET /api/search", h.search)
	h.mux.HandleFunc("GET /api/context", h.context)
	h.mux.HandleFunc("POST /api/ask", h.ask)
	h.mux.HandleFunc("POST /api/ask/stream", h.askStream)
	h.mux.HandleFunc("POST /api/import", h.importMarkdown)
	h.mux.HandleFunc("GET /api/images/{id}", h.image)
	h.mux.HandleFunc("GET /api/settings", h.getSettings)
	h.mux.HandleFunc("PUT /api/settings", h.putSettings)
	h.mux.HandleFunc("POST /api/settings/test", h.testSettings)
	h.mux.HandleFunc("GET /api/index/embeddings/status", h.embeddingIndexStatus)
	h.mux.HandleFunc("POST /api/index/embeddings/stream", h.buildEmbeddingIndex)

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	h.mux.Handle("/", http.FileServer(http.FS(static)))
}

type settingsProviderPayload struct {
	BaseURL      string `json:"base_url"`
	Model        string `json:"model"`
	Timeout      string `json:"timeout"`
	APIKey       string `json:"api_key,omitempty"`
	APIKeySet    bool   `json:"api_key_set"`
	APIKeyAction string `json:"api_key_action,omitempty"`
}
type settingsEmbeddingPayload struct {
	settingsProviderPayload
	Dimensions       int    `json:"dimensions"`
	BatchSize        int    `json:"batch_size"`
	QueryInstruction string `json:"query_instruction"`
}
type settingsPayload struct {
	Provider  settingsProviderPayload  `json:"provider"`
	Embedding settingsEmbeddingPayload `json:"embedding"`
	Overrides []string                 `json:"environment_overrides"`
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	values, err := settings.Read(h.configPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	payload := settingsPayload{
		Provider:  settingsProviderPayload{BaseURL: values.Provider.BaseURL, Model: values.Provider.Model, Timeout: values.Provider.Timeout.String(), APIKeySet: values.Provider.APIKey != ""},
		Embedding: settingsEmbeddingPayload{settingsProviderPayload: settingsProviderPayload{BaseURL: values.Embedding.BaseURL, Model: values.Embedding.Model, Timeout: values.Embedding.Timeout.String(), APIKeySet: values.Embedding.APIKey != ""}, Dimensions: values.Embedding.Dimensions, BatchSize: values.Embedding.BatchSize, QueryInstruction: values.Embedding.QueryInstruction},
		Overrides: make([]string, 0),
	}
	for _, key := range []string{"DOGEAR_BASE_URL", "DOGEAR_API_KEY", "DOGEAR_MODEL", "DOGEAR_TIMEOUT", "DOGEAR_EMBEDDING_BASE_URL", "DOGEAR_EMBEDDING_API_KEY", "DOGEAR_EMBEDDING_MODEL", "DOGEAR_EMBEDDING_DIMENSIONS", "DOGEAR_EMBEDDING_BATCH_SIZE"} {
		if _, ok := os.LookupEnv(key); ok {
			payload.Overrides = append(payload.Overrides, key)
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var payload settingsPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current, err := settings.Read(h.configPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	providerTimeout, err := time.ParseDuration(payload.Provider.Timeout)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid provider timeout: %w", err))
		return
	}
	embedTimeout, err := time.ParseDuration(payload.Embedding.Timeout)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid embedding timeout: %w", err))
		return
	}
	current.Provider.BaseURL = strings.TrimSpace(payload.Provider.BaseURL)
	current.Provider.Model = strings.TrimSpace(payload.Provider.Model)
	current.Provider.Timeout = providerTimeout
	current.Embedding.BaseURL = strings.TrimSpace(payload.Embedding.BaseURL)
	current.Embedding.Model = strings.TrimSpace(payload.Embedding.Model)
	current.Embedding.Timeout = embedTimeout
	current.Embedding.Dimensions = payload.Embedding.Dimensions
	current.Embedding.BatchSize = payload.Embedding.BatchSize
	current.Embedding.QueryInstruction = strings.TrimSpace(payload.Embedding.QueryInstruction)
	if err := applyKeyAction(&current.Provider.APIKey, payload.Provider.APIKeyAction, payload.Provider.APIKey); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := applyKeyAction(&current.Embedding.APIKey, payload.Embedding.APIKeyAction, payload.Embedding.APIKey); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := settings.Write(h.configPath, current); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	h.logger.InfoContext(r.Context(), "settings updated")
	h.getSettings(w, r)
}

func applyKeyAction(current *string, action, value string) error {
	switch action {
	case "", "preserve":
		return nil
	case "clear":
		*current = ""
		return nil
	case "replace":
		if strings.TrimSpace(value) == "" {
			return errors.New("replacement API key is empty")
		}
		*current = strings.TrimSpace(value)
		return nil
	default:
		return fmt.Errorf("invalid API key action %q", action)
	}
}

func (h *Handler) resolvedEmbedding(ctx context.Context) (embedding.Config, error) {
	provider, err := app.ProviderConfig(h.configPath, app.ProviderOverride{})
	if err != nil {
		return embedding.Config{}, err
	}
	return embedding.Resolve(h.configPath, provider.BaseURL, provider.APIKey)
}

func (h *Handler) testSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Target    string                    `json:"target"`
		Provider  *settingsProviderPayload  `json:"provider,omitempty"`
		Embedding *settingsEmbeddingPayload `json:"embedding,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Target != "provider" && request.Target != "embedding" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("target must be provider or embedding"))
		return
	}
	if request.Target == "embedding" {
		config, err := h.resolvedEmbedding(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if request.Embedding != nil {
			config.BaseURL = strings.TrimSpace(request.Embedding.BaseURL)
			config.Model = strings.TrimSpace(request.Embedding.Model)
			config.Dimensions = request.Embedding.Dimensions
			config.BatchSize = request.Embedding.BatchSize
			config.QueryInstruction = strings.TrimSpace(request.Embedding.QueryInstruction)
			if config.Dimensions < 32 || config.Dimensions > 4096 {
				writeError(w, http.StatusBadRequest, fmt.Errorf("embedding dimensions must be between 32 and 4096"))
				return
			}
			if config.BatchSize < 1 || config.BatchSize > 256 {
				writeError(w, http.StatusBadRequest, fmt.Errorf("embedding batch size must be between 1 and 256"))
				return
			}
			if request.Embedding.Timeout != "" {
				config.Timeout, err = time.ParseDuration(request.Embedding.Timeout)
				if err != nil {
					writeError(w, http.StatusBadRequest, fmt.Errorf("invalid embedding timeout: %w", err))
					return
				}
			}
			if err := applyKeyAction(&config.APIKey, request.Embedding.APIKeyAction, request.Embedding.APIKey); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		client, err := embedding.NewClient(config)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		vectors, err := client.Embed(r.Context(), []string{"DogEar embedding connectivity test"})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": config.Model, "dimensions": len(vectors[0])})
		return
	}
	provider, err := app.ProviderConfig(h.configPath, app.ProviderOverride{})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Provider != nil {
		provider.BaseURL = strings.TrimSpace(request.Provider.BaseURL)
		provider.Model = strings.TrimSpace(request.Provider.Model)
		if request.Provider.Timeout != "" {
			provider.Timeout, err = time.ParseDuration(request.Provider.Timeout)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid provider timeout: %w", err))
				return
			}
		}
		if err := applyKeyAction(&provider.APIKey, request.Provider.APIKeyAction, request.Provider.APIKey); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if err := testModelEndpoint(r.Context(), provider); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": provider.Model})
}

func testModelEndpoint(ctx context.Context, config llm.Config) error {
	u, err := url.Parse(config.BaseURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/chat/completions"), "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	if config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	client := &http.Client{Timeout: config.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("model endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (h *Handler) embeddingIndexStatus(w http.ResponseWriter, r *http.Request) {
	config, err := h.resolvedEmbedding(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := h.store.EmbeddingStatus(r.Context(), config.Model, config.Dimensions, config.IndexHash())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) documentHealth(w http.ResponseWriter, r *http.Request) {
	config, err := h.resolvedEmbedding(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := h.store.EmbeddingStatus(r.Context(), config.Model, config.Dimensions, config.IndexHash())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	health, err := h.store.DocumentHealth(r.Context(), r.PathValue("id"), status)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (h *Handler) buildEmbeddingIndex(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming is not supported"))
		return
	}
	config, err := h.resolvedEmbedding(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	client, err := embedding.NewClient(config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	status, err := h.store.BuildEmbeddingIndex(r.Context(), config.Model, config.Dimensions, config.BatchSize, config.IndexHash(), parseBool(r.URL.Query().Get("force")), client.Embed, func(indexed, total int) {
		_ = writeSSE(w, "progress", map[string]int{"indexed": indexed, "total": total})
		flusher.Flush()
	})
	if err != nil {
		_ = writeSSE(w, "error", errorResponse{Error: err.Error()})
		flusher.Flush()
		return
	}
	_ = writeSSE(w, "result", status)
	flusher.Flush()
}

func (h *Handler) documentChunks(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	chunks, err := h.store.DocumentChunks(r.Context(), r.PathValue("id"), after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, documentChunkResponses(chunks))
}

func (h *Handler) documentChunk(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("chunkID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid chunk id"))
		return
	}
	chunk, err := h.store.DocumentChunk(r.Context(), r.PathValue("id"), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, documentChunkResponseFor(chunk))
}

func (h *Handler) importMarkdown(w http.ResponseWriter, r *http.Request) {
	const maxFileBytes = 100 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxFileBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid multipart upload: %w", err))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("file is required: %w", err))
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(content) > maxFileBytes {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Markdown file exceeds %d bytes", maxFileBytes))
		return
	}
	meta := dogear.ImportMetadata{
		ID: strings.TrimSpace(r.FormValue("id")), Title: strings.TrimSpace(r.FormValue("title")),
		Brand: strings.TrimSpace(r.FormValue("brand")), Model: strings.TrimSpace(r.FormValue("model")),
		Version: strings.TrimSpace(r.FormValue("version")),
	}
	if tags := strings.TrimSpace(r.FormValue("tags")); tags != "" {
		meta.Tags = strings.Split(tags, ",")
	}
	result, err := dogear.ImportMarkdown(r.Context(), h.store, filepath.Base(header.Filename), content, meta, parseBool(r.FormValue("replace")))
	if err != nil {
		h.logger.WarnContext(r.Context(), "markdown import failed", "filename", filepath.Base(header.Filename), "error", err)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	h.logger.InfoContext(r.Context(), "markdown imported", "filename", filepath.Base(header.Filename), "documents", result.Documents, "chunks", result.Chunks, "images", result.Images)
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) image(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid image id"))
		return
	}
	image, err := h.store.Image(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	etag := `"` + image.ContentHash + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", image.MediaType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(image.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image.Data)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{OK: true})
}

func (h *Handler) documents(w http.ResponseWriter, r *http.Request) {
	documents, err := h.store.ListDocuments(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, documentInfoResponses(documents))
}

func (h *Handler) document(w http.ResponseWriter, r *http.Request) {
	info, err := h.store.DocumentInfo(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, documentInfoResponseFor(info))
}

func (h *Handler) removeDocument(w http.ResponseWriter, r *http.Request) {
	if err := h.store.RemoveDocument(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	h.logger.InfoContext(r.Context(), "document removed", "document_id", r.PathValue("id"))
	writeJSON(w, http.StatusOK, healthResponse{OK: true})
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("q is required"))
		return
	}
	limit, err := parseLimit(r, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	debug := parseBool(r.URL.Query().Get("debug"))
	results, err := h.rawRetriever.SearchRaw(r.Context(), dogear.SearchOptions{
		Query:      query,
		DocumentID: strings.TrimSpace(r.URL.Query().Get("doc")),
		Limit:      limit,
		Debug:      debug,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, searchResultResponses(results, debug))
}

func (h *Handler) context(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("q is required"))
		return
	}
	limit, err := parseLimit(r, 8)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	debug := parseBool(r.URL.Query().Get("debug"))
	result, err := h.rawRetriever.RetrieveRaw(r.Context(), dogear.RetrieveOptions{
		Query:      query,
		DocumentID: strings.TrimSpace(r.URL.Query().Get("doc")),
		Limit:      limit,
		Debug:      debug,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, retrievalResultResponseFor(result, debug))
}

func (h *Handler) ask(w http.ResponseWriter, r *http.Request) {
	var request askRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	request.Question = strings.TrimSpace(request.Question)
	if request.Question == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("question is required"))
		return
	}
	result, err := app.Ask(r.Context(), h.retriever, h.askOptions(request))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "ask failed", "stream", false, "error", err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	h.logger.InfoContext(r.Context(), "ask completed", "stream", false, "dry_run", request.DryRun, "model", result.Model, "sources", len(result.Sources))
	writeJSON(w, http.StatusOK, askResponse{
		Answer:      result.Answer,
		Model:       result.Model,
		ProviderURL: result.ProviderURL,
		Sources:     result.Sources,
		Retrieval:   result.Retrieval,
		Images:      result.Images,
		DryRun:      result.DryRun,
	})
}

func (h *Handler) askStream(w http.ResponseWriter, r *http.Request) {
	var request askRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	request.Question = strings.TrimSpace(request.Question)
	if request.Question == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("question is required"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	result, err := app.AskStream(r.Context(), h.retriever, h.askOptions(request), func(delta string) error {
		if err := writeSSE(w, "delta", map[string]string{"content": delta}); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "ask failed", "stream", true, "error", err)
		_ = writeSSE(w, "error", errorResponse{Error: err.Error()})
		flusher.Flush()
		return
	}
	h.logger.InfoContext(r.Context(), "ask completed", "stream", true, "model", result.Model, "sources", len(result.Sources))
	_ = writeSSE(w, "result", askResponse{
		Answer: result.Answer, Model: result.Model, ProviderURL: result.ProviderURL,
		Sources: result.Sources, Retrieval: result.Retrieval, Images: result.Images,
	})
	flusher.Flush()
}

func (h *Handler) askOptions(request askRequest) app.AskOptions {
	return app.AskOptions{
		Question:   request.Question,
		DocumentID: strings.TrimSpace(request.Document),
		Limit:      request.Limit,
		DryRun:     request.DryRun,
		ConfigPath: h.configPath,
		Provider: app.ProviderOverride{
			BaseURL: request.BaseURL,
			APIKey:  request.APIKey,
			Model:   request.Model,
			Timeout: request.Timeout,
		},
		History: request.History,
	}
}

func writeSSE(w io.Writer, event string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

func parseLimit(r *http.Request, fallback int) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	return limit, nil
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

func Serve(ctx context.Context, addr string, store *dogear.Store, configPath string, logger *slog.Logger) error {
	if logger == nil {
		logger = logging.Discard()
	}
	server := &http.Server{
		Addr:    addr,
		Handler: New(Options{Store: store, ConfigPath: configPath, Logger: logger}),
	}
	logger.InfoContext(ctx, "server starting", "addr", addr)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		if err := server.Shutdown(context.Background()); err != nil {
			logger.Error("server shutdown failed", "error", err)
			return err
		}
		logger.Info("server stopped", "reason", "context cancelled")
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			logger.Info("server stopped", "reason", "closed")
			return nil
		}
		logger.Error("server failed", "error", err)
		return err
	}
}
