package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/retrievalpolicy"
)

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("q is required"))
		return
	}
	limit, err := parseLimit(r, retrievalpolicy.DefaultSearchLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	debug := parseBool(r.URL.Query().Get("debug"))
	results, err := h.rawRetriever.SearchRaw(r.Context(), dogear.SearchOptions{Query: query, DocumentID: strings.TrimSpace(r.URL.Query().Get("doc")), Limit: limit, Debug: debug})
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
	limit, err := parseLimit(r, retrievalpolicy.DefaultContextLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	debug := parseBool(r.URL.Query().Get("debug"))
	result, err := h.rawRetriever.RetrieveRaw(r.Context(), dogear.RetrieveOptions{Query: query, DocumentID: strings.TrimSpace(r.URL.Query().Get("doc")), Limit: limit, Debug: debug})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, retrievalResultResponseFor(result, debug))
}

func (h *Handler) ask(w http.ResponseWriter, r *http.Request) {
	var request askRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	request.Question = strings.TrimSpace(request.Question)
	if request.Question == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("question is required"))
		return
	}
	if !h.acquireAskSlot(w) {
		return
	}
	defer h.releaseAskSlot()
	result, err := app.Ask(r.Context(), h.retriever, h.askOptions(request))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "ask failed", "stream", false, "error", err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	h.logger.InfoContext(r.Context(), "ask completed", "stream", false, "dry_run", request.DryRun, "model", result.Model, "sources", len(result.Sources))
	writeJSON(w, http.StatusOK, askResponse{Answer: result.Answer, Model: result.Model, ProviderURL: result.ProviderURL, Sources: result.Sources, Retrieval: result.Retrieval, Images: result.Images, DryRun: result.DryRun, Mode: result.Mode})
}

func (h *Handler) askStream(w http.ResponseWriter, r *http.Request) {
	var request askRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	request.Question = strings.TrimSpace(request.Question)
	if request.Question == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("question is required"))
		return
	}
	if !h.acquireAskSlot(w) {
		return
	}
	defer h.releaseAskSlot()
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
	options := h.askOptions(request)
	options.OnStatus = func(status string) error {
		if err := writeSSE(w, "status", map[string]string{"message": status}); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	result, err := app.AskStream(r.Context(), h.retriever, options, func(delta string) error {
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
	_ = writeSSE(w, "result", askResponse{Answer: result.Answer, Model: result.Model, ProviderURL: result.ProviderURL, Sources: result.Sources, Retrieval: result.Retrieval, Images: result.Images, Mode: result.Mode})
	flusher.Flush()
}

func (h *Handler) acquireAskSlot(w http.ResponseWriter) bool {
	select {
	case h.askSlots <- struct{}{}:
		return true
	default:
		writeError(w, http.StatusTooManyRequests, errors.New("too many concurrent answer requests"))
		return false
	}
}
func (h *Handler) releaseAskSlot() { <-h.askSlots }
func (h *Handler) acquireEmbeddingSlot(w http.ResponseWriter) bool {
	select {
	case h.embeddingSlots <- struct{}{}:
		return true
	default:
		writeError(w, http.StatusTooManyRequests, errors.New("embedding provider is busy"))
		return false
	}
}
func (h *Handler) releaseEmbeddingSlot() { <-h.embeddingSlots }
func (h *Handler) askOptions(request askRequest) app.AskOptions {
	return app.AskOptions{Question: request.Question, DocumentID: strings.TrimSpace(request.Document), Limit: request.Limit, DryRun: request.DryRun, ConfigPath: h.configPath, Provider: app.ProviderOverride{BaseURL: request.BaseURL, APIKey: request.APIKey, Model: request.Model, Timeout: request.Timeout}, History: request.History, Mode: request.Mode}
}
