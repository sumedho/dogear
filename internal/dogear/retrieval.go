package dogear

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sumedho/dogear/internal/retrievalpolicy"
)

func (s *Store) Retrieve(ctx context.Context, opts RetrieveOptions) (RetrievalResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = retrievalpolicy.DefaultContextLimit
	}
	query := NormalizeFTSQuery(opts.Query)
	if query == "" {
		return RetrievalResult{}, errors.New("empty retrieval query")
	}

	result, err := s.retrieveWithQuery(ctx, opts, query, opts.Limit*retrievalpolicy.CandidateMultiplier)
	if err != nil {
		return RetrievalResult{}, err
	}
	if len(result.Blocks) < opts.Limit && strings.Contains(query, " AND ") {
		fallback, err := s.retrieveWithQuery(ctx, opts, strings.ReplaceAll(query, " AND ", " OR "), opts.Limit*retrievalpolicy.CandidateMultiplier)
		if err != nil {
			return RetrievalResult{}, err
		}
		result.Blocks = mergeBlocks(result.Blocks, fallback.Blocks)
	}
	chunks := make([]RetrievedChunk, 0, len(result.Blocks))
	for _, block := range result.Blocks {
		chunks = append(chunks, RetrievedChunk{
			ChunkID:     block.Source.ChunkID,
			DocumentID:  block.Source.DocumentID,
			Title:       block.Source.Title,
			Brand:       block.Source.Brand,
			Model:       block.Source.Model,
			HeadingPath: block.Source.HeadingPath,
			PageNumber:  block.Source.PageNumber,
			StartLine:   block.Source.StartLine,
			EndLine:     block.Source.EndLine,
			Text:        block.Text,
			Score:       block.Source.Score,
		})
	}
	reranked := rerankChunks(opts.Query, chunks, opts.Limit)
	out := RetrievalResult{Query: opts.Query, Blocks: make([]ContextBlock, 0, len(reranked))}
	for i, chunk := range reranked {
		chunk.Debug = rankCandidate(chunk, uniqueTerms(tokenize(NormalizeFTSQuery(opts.Query))))
		source := SourceRef{
			ChunkID:     chunk.ChunkID,
			Label:       fmt.Sprintf("[%d]", i+1),
			DocumentID:  chunk.DocumentID,
			Title:       chunk.Title,
			Brand:       chunk.Brand,
			Model:       chunk.Model,
			HeadingPath: chunk.HeadingPath,
			PageNumber:  chunk.PageNumber,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Score:       chunk.Score,
			Debug:       chunk.Debug,
		}
		images, err := s.imagesForChunk(ctx, chunk.ChunkID)
		if err != nil {
			return RetrievalResult{}, err
		}
		out.Blocks = append(out.Blocks, ContextBlock{Source: source, Text: chunk.Text, Images: images})
	}
	return out, nil
}

type hybridRanks struct {
	ftsRank    int
	vectorRank int
	distance   float64
	fused      float64
}

func (s *Store) RetrieveHybrid(ctx context.Context, opts RetrieveOptions, queryVector []float32) (RetrievalResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = retrievalpolicy.DefaultContextLimit
	}
	query := NormalizeFTSQuery(opts.Query)
	if query == "" {
		return RetrievalResult{}, errors.New("empty retrieval query")
	}
	fetchLimit := opts.Limit * retrievalpolicy.CandidateMultiplier
	lexical, err := s.retrieveWithQuery(ctx, opts, query, fetchLimit)
	if err != nil {
		return RetrievalResult{}, err
	}
	if len(lexical.Blocks) < fetchLimit && strings.Contains(query, " AND ") {
		fallback, err := s.retrieveWithQuery(ctx, opts, strings.ReplaceAll(query, " AND ", " OR "), fetchLimit)
		if err != nil {
			return RetrievalResult{}, err
		}
		lexical.Blocks = mergeBlocks(lexical.Blocks, fallback.Blocks)
	}
	rawVector, err := json.Marshal(queryVector)
	if err != nil {
		return RetrievalResult{}, err
	}
	querySQL := `SELECT rowid, distance FROM chunk_embeddings WHERE embedding MATCH ? AND k = ?`
	args := []any{string(rawVector), fetchLimit}
	if opts.DocumentID != "" {
		querySQL += ` AND document_id = ?`
		args = append(args, opts.DocumentID)
	}
	querySQL += ` ORDER BY distance`
	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return RetrievalResult{}, err
	}
	type vectorHit struct {
		id       int64
		distance float64
	}
	var vectorHits []vectorHit
	for rows.Next() {
		var hit vectorHit
		if err := rows.Scan(&hit.id, &hit.distance); err != nil {
			rows.Close()
			return RetrievalResult{}, err
		}
		vectorHits = append(vectorHits, hit)
	}
	if err := rows.Close(); err != nil {
		return RetrievalResult{}, err
	}

	chunks := map[int64]RetrievedChunk{}
	ranks := map[int64]*hybridRanks{}
	for i, block := range lexical.Blocks {
		id := block.Source.ChunkID
		chunks[id] = RetrievedChunk{ChunkID: id, DocumentID: block.Source.DocumentID, Title: block.Source.Title, Brand: block.Source.Brand, Model: block.Source.Model, HeadingPath: block.Source.HeadingPath, PageNumber: block.Source.PageNumber, StartLine: block.Source.StartLine, EndLine: block.Source.EndLine, Text: block.Text, Score: block.Source.Score}
		ranks[id] = &hybridRanks{ftsRank: i + 1}
	}
	for i, hit := range vectorHits {
		rank := ranks[hit.id]
		if rank == nil {
			rank = &hybridRanks{}
			ranks[hit.id] = rank
		}
		rank.vectorRank = i + 1
		rank.distance = hit.distance
		if _, ok := chunks[hit.id]; !ok {
			chunk, err := s.retrievedChunk(ctx, hit.id)
			if err != nil {
				return RetrievalResult{}, err
			}
			chunks[hit.id] = chunk
		}
	}
	candidates := make([]RetrievedChunk, 0, len(chunks))
	for id, chunk := range chunks {
		rank := ranks[id]
		if rank.ftsRank > 0 {
			rank.fused += 1 / (retrievalpolicy.ReciprocalRankK + float64(rank.ftsRank))
		}
		if rank.vectorRank > 0 {
			rank.fused += 1 / (retrievalpolicy.ReciprocalRankK + float64(rank.vectorRank))
		}
		chunk.Score = -rank.fused * 100
		candidates = append(candidates, chunk)
	}
	ranked := rerankChunks(opts.Query, candidates, opts.Limit)
	out := RetrievalResult{Query: opts.Query, Blocks: make([]ContextBlock, 0, len(ranked))}
	for i, chunk := range ranked {
		rank := ranks[chunk.ChunkID]
		debug := chunk.Debug
		debug.Mode = "hybrid"
		debug.FTSRank = rank.ftsRank
		debug.VectorRank = rank.vectorRank
		debug.VectorDistance = rank.distance
		debug.FusedScore = rank.fused
		source := SourceRef{ChunkID: chunk.ChunkID, Label: fmt.Sprintf("[%d]", i+1), DocumentID: chunk.DocumentID, Title: chunk.Title, Brand: chunk.Brand, Model: chunk.Model, HeadingPath: chunk.HeadingPath, PageNumber: chunk.PageNumber, StartLine: chunk.StartLine, EndLine: chunk.EndLine, Score: chunk.Score, Debug: debug}
		images, err := s.imagesForChunk(ctx, chunk.ChunkID)
		if err != nil {
			return RetrievalResult{}, err
		}
		out.Blocks = append(out.Blocks, ContextBlock{Source: source, Text: chunk.Text, Images: images})
	}
	return out, nil
}

func (s *Store) retrievedChunk(ctx context.Context, id int64) (RetrievedChunk, error) {
	var chunk RetrievedChunk
	err := s.db.QueryRowContext(ctx, `SELECT c.id, c.document_id, d.title, d.brand, d.model, c.heading_path, c.page_number, c.start_line, c.end_line, c.text
		FROM chunks c JOIN documents d ON d.id = c.document_id WHERE c.id = ?`, id).
		Scan(&chunk.ChunkID, &chunk.DocumentID, &chunk.Title, &chunk.Brand, &chunk.Model, &chunk.HeadingPath, &chunk.PageNumber, &chunk.StartLine, &chunk.EndLine, &chunk.Text)
	return chunk, err
}

// AdjacentContext returns nearby searchable sections from the same document.
func (s *Store) AdjacentContext(ctx context.Context, chunkID int64, limit int) ([]ContextBlock, error) {
	if limit <= 0 || limit > 4 {
		limit = 1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT nearby.id FROM chunks target
		JOIN chunks nearby ON nearby.document_id = target.document_id AND nearby.id <> target.id
		WHERE target.id = ? ORDER BY ABS(nearby.ordinal - target.ordinal), nearby.ordinal LIMIT ?`, chunkID, limit*4)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	blocks := make([]ContextBlock, 0, limit)
	for _, id := range ids {
		chunk, err := s.retrievedChunk(ctx, id)
		if err != nil {
			return nil, err
		}
		if !isSearchableSection(chunk.HeadingPath, chunk.Text) {
			continue
		}
		images, err := s.imagesForChunk(ctx, id)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, ContextBlock{Source: SourceRef{ChunkID: chunk.ChunkID, DocumentID: chunk.DocumentID, Title: chunk.Title, Brand: chunk.Brand, Model: chunk.Model, HeadingPath: chunk.HeadingPath, PageNumber: chunk.PageNumber, StartLine: chunk.StartLine, EndLine: chunk.EndLine, Score: chunk.Score}, Text: chunk.Text, Images: images})
		if len(blocks) == limit {
			break
		}
	}
	return blocks, nil
}

func (s *Store) retrieveWithQuery(ctx context.Context, opts RetrieveOptions, query string, fetchLimit int) (RetrievalResult, error) {
	if fetchLimit <= 0 {
		fetchLimit = opts.Limit
	}
	args := []any{query}
	where := `chunks_fts MATCH ?`
	if opts.DocumentID != "" {
		where += ` AND f.document_id = ?`
		args = append(args, opts.DocumentID)
	}
	args = append(args, fetchLimit)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT c.id, f.document_id, d.title, d.brand, d.model, c.heading_path, c.page_number,
			c.start_line, c.end_line, c.text, bm25(chunks_fts) AS score
		FROM chunks_fts f
		JOIN chunks c ON c.id = f.chunk_id
		JOIN documents d ON d.id = f.document_id
		WHERE %s
		ORDER BY score
		LIMIT ?`, where), args...)
	if err != nil {
		return RetrievalResult{}, err
	}
	defer rows.Close()

	result := RetrievalResult{Query: opts.Query}
	for rows.Next() {
		var chunk RetrievedChunk
		if err := rows.Scan(&chunk.ChunkID, &chunk.DocumentID, &chunk.Title, &chunk.Brand, &chunk.Model, &chunk.HeadingPath, &chunk.PageNumber, &chunk.StartLine, &chunk.EndLine, &chunk.Text, &chunk.Score); err != nil {
			return RetrievalResult{}, err
		}
		if !isSearchableSection(chunk.HeadingPath, chunk.Text) {
			continue
		}
		source := SourceRef{
			ChunkID:     chunk.ChunkID,
			Label:       fmt.Sprintf("[%d]", len(result.Blocks)+1),
			DocumentID:  chunk.DocumentID,
			Title:       chunk.Title,
			Brand:       chunk.Brand,
			Model:       chunk.Model,
			HeadingPath: chunk.HeadingPath,
			PageNumber:  chunk.PageNumber,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Score:       chunk.Score,
		}
		result.Blocks = append(result.Blocks, ContextBlock{Source: source, Text: chunk.Text})
	}
	return result, rows.Err()
}

func mergeBlocks(primary, secondary []ContextBlock) []ContextBlock {
	seen := map[int64]bool{}
	out := make([]ContextBlock, 0, len(primary)+len(secondary))
	for _, block := range primary {
		if seen[block.Source.ChunkID] {
			continue
		}
		seen[block.Source.ChunkID] = true
		out = append(out, block)
	}
	for _, block := range secondary {
		if seen[block.Source.ChunkID] {
			continue
		}
		seen[block.Source.ChunkID] = true
		out = append(out, block)
	}
	return out
}
