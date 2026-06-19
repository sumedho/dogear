package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/llm"
)

//go:embed static/*
var staticFiles embed.FS

type Options struct {
	Store      *dogear.Store
	ConfigPath string
}

type Handler struct {
	store      *dogear.Store
	configPath string
	mux        *http.ServeMux
}

type askRequest struct {
	Question string `json:"question"`
	Document string `json:"doc"`
	Limit    int    `json:"limit"`
	DryRun   bool   `json:"dry_run"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	Timeout  string `json:"timeout"`
}

type askResponse struct {
	Answer      string              `json:"answer,omitempty"`
	Model       string              `json:"model"`
	ProviderURL string              `json:"provider_url"`
	Sources     []app.SourceRef     `json:"sources"`
	Retrieval   app.RetrievalResult `json:"retrieval"`
	DryRun      *llm.DryRun         `json:"dry_run,omitempty"`
}

type healthResponse struct {
	OK bool `json:"ok"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func New(options Options) http.Handler {
	handler := &Handler{
		store:      options.Store,
		configPath: options.ConfigPath,
		mux:        http.NewServeMux(),
	}
	handler.routes()
	return handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
	h.mux.HandleFunc("GET /api/health", h.health)
	h.mux.HandleFunc("GET /api/documents", h.documents)
	h.mux.HandleFunc("GET /api/documents/{id}", h.document)
	h.mux.HandleFunc("GET /api/search", h.search)
	h.mux.HandleFunc("GET /api/context", h.context)
	h.mux.HandleFunc("POST /api/ask", h.ask)

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	h.mux.Handle("/", http.FileServer(http.FS(static)))
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
	writeJSON(w, http.StatusOK, app.DocumentInfoResponses(documents))
}

func (h *Handler) document(w http.ResponseWriter, r *http.Request) {
	info, err := h.store.DocumentInfo(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, app.DocumentInfoResponse(info))
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
	results, err := h.store.Search(r.Context(), dogear.SearchOptions{
		Query:      query,
		DocumentID: strings.TrimSpace(r.URL.Query().Get("doc")),
		Limit:      limit,
		Debug:      debug,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, app.SearchResponses(results, debug))
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
	result, err := h.store.Retrieve(r.Context(), dogear.RetrieveOptions{
		Query:      query,
		DocumentID: strings.TrimSpace(r.URL.Query().Get("doc")),
		Limit:      limit,
		Debug:      debug,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, app.RetrievalResponse(result, debug))
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
	result, err := app.Ask(r.Context(), h.store, app.AskOptions{
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
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, askResponse{
		Answer:      result.Answer,
		Model:       result.Model,
		ProviderURL: result.ProviderURL,
		Sources:     result.Sources,
		Retrieval:   result.Retrieval,
		DryRun:      result.DryRun,
	})
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

func Serve(ctx context.Context, addr string, store *dogear.Store, configPath string) error {
	server := &http.Server{
		Addr:    addr,
		Handler: New(Options{Store: store, ConfigPath: configPath}),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		if err := server.Shutdown(context.Background()); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
