package dogear

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sumedho/dogear/internal/retrievalpolicy"
)

func (s *Store) Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = retrievalpolicy.DefaultSearchLimit
	}
	query := NormalizeFTSQuery(opts.Query)
	if query == "" {
		return nil, errors.New("empty search query")
	}

	fetchLimit := opts.Limit * retrievalpolicy.CandidateMultiplier
	candidates, err := s.searchWithQuery(ctx, opts, query, fetchLimit)
	if err != nil {
		return nil, err
	}
	if len(candidates) < opts.Limit && strings.Contains(query, " AND ") {
		fallback, err := s.searchWithQuery(ctx, opts, strings.ReplaceAll(query, " AND ", " OR "), fetchLimit)
		if err != nil {
			return nil, err
		}
		candidates = mergeSearchResults(candidates, fallback)
	}
	chunks := make([]RetrievedChunk, 0, len(candidates))
	snippets := map[int64]string{}
	for _, candidate := range candidates {
		chunks = append(chunks, RetrievedChunk{
			ChunkID:     candidate.ChunkID,
			DocumentID:  candidate.DocumentID,
			Title:       candidate.Title,
			HeadingPath: candidate.HeadingPath,
			PageNumber:  candidate.PageNumber,
			StartLine:   candidate.StartLine,
			EndLine:     candidate.EndLine,
			Text:        candidate.Snippet,
			Score:       candidate.Score,
		})
		snippets[candidate.ChunkID] = candidate.Snippet
	}
	reranked := rerankChunks(opts.Query, chunks, opts.Limit)
	out := make([]SearchResult, 0, len(reranked))
	for _, chunk := range reranked {
		images, err := s.imagesForChunk(ctx, chunk.ChunkID)
		if err != nil {
			return nil, err
		}
		out = append(out, SearchResult{
			ChunkID:     chunk.ChunkID,
			DocumentID:  chunk.DocumentID,
			Title:       chunk.Title,
			HeadingPath: chunk.HeadingPath,
			PageNumber:  chunk.PageNumber,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Snippet:     snippets[chunk.ChunkID],
			Images:      images,
			Score:       chunk.Score,
			Debug:       chunk.Debug,
		})
	}
	return out, nil
}

func (s *Store) SearchHybrid(ctx context.Context, opts SearchOptions, queryVector []float32) ([]SearchResult, error) {
	retrieval, err := s.RetrieveHybrid(ctx, RetrieveOptions{Query: opts.Query, DocumentID: opts.DocumentID, Limit: opts.Limit, Debug: opts.Debug}, queryVector)
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(retrieval.Blocks))
	for _, block := range retrieval.Blocks {
		results = append(results, SearchResult{ChunkID: block.Source.ChunkID, DocumentID: block.Source.DocumentID, Title: block.Source.Title,
			HeadingPath: block.Source.HeadingPath, PageNumber: block.Source.PageNumber, StartLine: block.Source.StartLine, EndLine: block.Source.EndLine,
			Snippet: block.Text, Images: block.Images, Score: block.Source.Score, Debug: block.Source.Debug})
	}
	return results, nil
}

func (s *Store) searchWithQuery(ctx context.Context, opts SearchOptions, query string, fetchLimit int) ([]SearchResult, error) {
	args := []any{query}
	where := `chunks_fts MATCH ?`
	if opts.DocumentID != "" {
		where += ` AND f.document_id = ?`
		args = append(args, opts.DocumentID)
	}
	args = append(args, fetchLimit)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT c.id, f.document_id, d.title, c.heading_path, c.page_number,
			c.start_line, c.end_line,
			snippet(chunks_fts, 6, '[', ']', ' ... ', 20) AS snippet,
			bm25(chunks_fts) AS score
		FROM chunks_fts f
		JOIN chunks c ON c.id = f.chunk_id
		JOIN documents d ON d.id = f.document_id
		WHERE %s
		ORDER BY score
		LIMIT ?`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(&result.ChunkID, &result.DocumentID, &result.Title, &result.HeadingPath, &result.PageNumber, &result.StartLine, &result.EndLine, &result.Snippet, &result.Score); err != nil {
			return nil, err
		}
		if !isSearchableSection(result.HeadingPath, result.Snippet) {
			continue
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func mergeSearchResults(primary, secondary []SearchResult) []SearchResult {
	seen := map[int64]bool{}
	out := make([]SearchResult, 0, len(primary)+len(secondary))
	for _, result := range primary {
		if seen[result.ChunkID] {
			continue
		}
		seen[result.ChunkID] = true
		out = append(out, result)
	}
	for _, result := range secondary {
		if seen[result.ChunkID] {
			continue
		}
		seen[result.ChunkID] = true
		out = append(out, result)
	}
	return out
}
