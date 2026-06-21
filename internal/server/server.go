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
	Context    context.Context
}

type Handler struct {
	store          *dogear.Store
	retriever      app.Retriever
	rawRetriever   dogearadapter.Retriever
	configPath     string
	logger         *slog.Logger
	mux            *http.ServeMux
	lifecycle      context.Context
	embeddingJob   embeddingJobManager
	askSlots       chan struct{}
	embeddingSlots chan struct{}
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
	Mode     app.ResponseMode          `json:"mode,omitempty"`
}

type askResponse struct {
	Answer      string              `json:"answer,omitempty"`
	Model       string              `json:"model"`
	ProviderURL string              `json:"provider_url"`
	Sources     []app.SourceRef     `json:"sources"`
	Retrieval   app.RetrievalResult `json:"retrieval"`
	Images      []app.DisplayImage  `json:"images,omitempty"`
	DryRun      *llm.DryRun         `json:"dry_run,omitempty"`
	Mode        app.ResponseMode    `json:"mode"`
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
	lifecycle := options.Context
	if lifecycle == nil {
		lifecycle = context.Background()
	}
	rawRetriever := dogearadapter.NewConfiguredRetriever(options.Store, options.ConfigPath)
	handler := &Handler{
		store:          options.Store,
		retriever:      rawRetriever,
		rawRetriever:   rawRetriever,
		configPath:     options.ConfigPath,
		logger:         logger,
		mux:            http.NewServeMux(),
		lifecycle:      lifecycle,
		askSlots:       make(chan struct{}, 2),
		embeddingSlots: make(chan struct{}, 1),
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
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current, err := settings.Read(h.configPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := applyProviderPayload(&current.Provider, payload.Provider, true); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := applyEmbeddingPayload(&current.Embedding, payload.Embedding, true); err != nil {
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
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Target != "provider" && request.Target != "embedding" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("target must be provider or embedding"))
		return
	}
	if request.Target == "embedding" {
		if !h.acquireEmbeddingSlot(w) {
			return
		}
		defer h.releaseEmbeddingSlot()
		config, err := h.resolvedEmbedding(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if request.Embedding != nil {
			if err := applyEmbeddingPayload(&config, *request.Embedding, false); err != nil {
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
		if err := applyProviderPayload(&provider, *request.Provider, false); err != nil {
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
	type response struct {
		dogear.EmbeddingIndexStatus
		Building        bool   `json:"building"`
		JobID           string `json:"job_id,omitempty"`
		ProgressIndexed int    `json:"progress_indexed,omitempty"`
		ProgressTotal   int    `json:"progress_total,omitempty"`
		LastError       string `json:"last_error,omitempty"`
	}
	payload := response{EmbeddingIndexStatus: status}
	if job, _, ok := h.embeddingJob.snapshot(""); ok {
		payload.Building = !job.Done
		payload.JobID = job.ID
		payload.ProgressIndexed = job.Indexed
		payload.ProgressTotal = job.Total
		if job.Err != nil {
			payload.LastError = job.Err.Error()
		}
	}
	writeJSON(w, http.StatusOK, payload)
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
	job, _, active := h.embeddingJob.snapshot("")
	jobID := ""
	if active && !job.Done {
		jobID = job.ID
	} else {
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
		force := parseBool(r.URL.Query().Get("force"))
		jobID = h.embeddingJob.start(h.lifecycle, func(ctx context.Context, progress func(int, int)) (dogear.EmbeddingIndexStatus, error) {
			select {
			case h.embeddingSlots <- struct{}{}:
				defer h.releaseEmbeddingSlot()
			case <-ctx.Done():
				return dogear.EmbeddingIndexStatus{}, ctx.Err()
			}
			return h.store.BuildEmbeddingIndex(ctx, config.Model, config.Dimensions, config.BatchSize, config.IndexHash(), force, client.Embed, progress)
		})
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	lastIndexed, lastTotal := -1, -1
	for {
		snapshot, changed, ok := h.embeddingJob.snapshot(jobID)
		if !ok {
			_ = writeSSE(w, "error", errorResponse{Error: "embedding job is unavailable"})
			flusher.Flush()
			return
		}
		if snapshot.Indexed != lastIndexed || snapshot.Total != lastTotal {
			_ = writeSSE(w, "progress", map[string]any{"job_id": snapshot.ID, "indexed": snapshot.Indexed, "total": snapshot.Total})
			flusher.Flush()
			lastIndexed, lastTotal = snapshot.Indexed, snapshot.Total
		}
		if snapshot.Done {
			if snapshot.Err != nil {
				_ = writeSSE(w, "error", errorResponse{Error: snapshot.Err.Error()})
			} else {
				_ = writeSSE(w, "result", snapshot.Result)
			}
			flusher.Flush()
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
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
		Addr:              addr,
		Handler:           New(Options{Store: store, ConfigPath: configPath, Logger: logger, Context: ctx}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	logger.InfoContext(ctx, "server starting", "addr", addr)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
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
