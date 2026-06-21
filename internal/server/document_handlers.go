package server

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sumedho/dogear/internal/dogear"
)

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
	meta := dogear.ImportMetadata{ID: strings.TrimSpace(r.FormValue("id")), Title: strings.TrimSpace(r.FormValue("title")), Brand: strings.TrimSpace(r.FormValue("brand")), Model: strings.TrimSpace(r.FormValue("model")), Version: strings.TrimSpace(r.FormValue("version"))}
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

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
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
