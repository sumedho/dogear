package dogearadapter

import (
	"context"

	"github.com/sumedho/dogear/internal/app"
	"github.com/sumedho/dogear/internal/dogear"
	"github.com/sumedho/dogear/internal/embedding"
	"github.com/sumedho/dogear/internal/sqlutil"
)

type Retriever struct {
	store      dogear.RetrievalStore
	configPath string
}

func NewRetriever(store dogear.RetrievalStore) Retriever {
	return Retriever{store: store}
}

func NewConfiguredRetriever(store dogear.RetrievalStore, configPath string) Retriever {
	return Retriever{store: store, configPath: configPath}
}

func (r Retriever) Retrieve(ctx context.Context, opts app.RetrieveOptions) (app.RetrievalResult, error) {
	dogearOpts := dogear.RetrieveOptions{
		Query:      opts.Query,
		DocumentID: opts.DocumentID,
		Limit:      opts.Limit,
	}
	result, err := r.retrieve(ctx, dogearOpts)
	if err != nil {
		return app.RetrievalResult{}, err
	}
	return retrievalResult(result), nil
}

func (r Retriever) retrieve(ctx context.Context, opts dogear.RetrieveOptions) (dogear.RetrievalResult, error) {
	if r.configPath == "" {
		return r.store.Retrieve(ctx, opts)
	}
	provider, err := app.ProviderConfig(r.configPath, app.ProviderOverride{})
	if err != nil {
		return dogear.RetrievalResult{}, err
	}
	config, err := embedding.Resolve(r.configPath, provider.BaseURL, provider.APIKey)
	if err != nil || config.Model == "" {
		result, retrieveErr := r.store.Retrieve(ctx, opts)
		setFallback(&result, "embedding is not configured")
		return result, retrieveErr
	}
	status, statusErr := r.store.EmbeddingStatus(ctx, config.Model, config.Dimensions, config.IndexHash())
	if statusErr != nil || !status.Complete {
		result, retrieveErr := r.store.Retrieve(ctx, opts)
		setFallback(&result, "embedding index is incomplete")
		return result, retrieveErr
	}
	client, err := embedding.NewClient(config)
	if err != nil {
		result, retrieveErr := r.store.Retrieve(ctx, opts)
		setFallback(&result, "embedding client is unavailable")
		return result, retrieveErr
	}
	vector, err := client.EmbedQuery(ctx, opts.Query)
	if err != nil {
		result, retrieveErr := r.store.Retrieve(ctx, opts)
		setFallback(&result, "embedding endpoint unavailable")
		return result, retrieveErr
	}
	return r.store.RetrieveHybrid(ctx, opts, vector)
}

func (r Retriever) RetrieveRaw(ctx context.Context, opts dogear.RetrieveOptions) (dogear.RetrievalResult, error) {
	return r.retrieve(ctx, opts)
}

func (r Retriever) SearchRaw(ctx context.Context, opts dogear.SearchOptions) ([]dogear.SearchResult, error) {
	if r.configPath == "" {
		return r.store.Search(ctx, opts)
	}
	provider, err := app.ProviderConfig(r.configPath, app.ProviderOverride{})
	if err != nil {
		return nil, err
	}
	config, err := embedding.Resolve(r.configPath, provider.BaseURL, provider.APIKey)
	if err != nil || config.Model == "" {
		results, searchErr := r.store.Search(ctx, opts)
		setSearchFallback(results, "embedding is not configured")
		return results, searchErr
	}
	status, err := r.store.EmbeddingStatus(ctx, config.Model, config.Dimensions, config.IndexHash())
	if err != nil || !status.Complete {
		results, searchErr := r.store.Search(ctx, opts)
		setSearchFallback(results, "embedding index is incomplete")
		return results, searchErr
	}
	client, err := embedding.NewClient(config)
	if err != nil {
		results, searchErr := r.store.Search(ctx, opts)
		setSearchFallback(results, "embedding client is unavailable")
		return results, searchErr
	}
	vector, err := client.EmbedQuery(ctx, opts.Query)
	if err != nil {
		results, searchErr := r.store.Search(ctx, opts)
		setSearchFallback(results, "embedding endpoint unavailable")
		return results, searchErr
	}
	return r.store.SearchHybrid(ctx, opts, vector)
}

func setSearchFallback(results []dogear.SearchResult, reason string) {
	for i := range results {
		results[i].Debug.Mode = "fts"
		results[i].Debug.FallbackReason = reason
	}
}

func setFallback(result *dogear.RetrievalResult, reason string) {
	for i := range result.Blocks {
		result.Blocks[i].Source.Debug.Mode = "fts"
		result.Blocks[i].Source.Debug.FallbackReason = reason
	}
}

func retrievalResult(result dogear.RetrievalResult) app.RetrievalResult {
	out := app.RetrievalResult{
		Query:  result.Query,
		Blocks: make([]app.ContextBlock, 0, len(result.Blocks)),
	}
	for _, block := range result.Blocks {
		images := make([]app.ImageRef, 0, len(block.Images))
		for _, image := range block.Images {
			images = append(images, app.ImageRef{ID: image.ID, Alt: image.Alt, MediaType: image.MediaType})
		}
		out.Blocks = append(out.Blocks, app.ContextBlock{
			Source: app.SourceRef{
				ChunkID:     block.Source.ChunkID,
				Label:       block.Source.Label,
				DocumentID:  block.Source.DocumentID,
				Title:       block.Source.Title,
				Brand:       block.Source.Brand,
				Model:       block.Source.Model,
				HeadingPath: block.Source.HeadingPath,
				PageNumber:  sqlutil.Int64Ptr(block.Source.PageNumber),
				StartLine:   block.Source.StartLine,
				EndLine:     block.Source.EndLine,
				Score:       block.Source.Score,
			},
			Text:   block.Text,
			Images: images,
		})
	}
	return out
}
