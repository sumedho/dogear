package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/sumedho/dogear/internal/embedding"
	"github.com/sumedho/dogear/internal/llm"
)

type Values struct {
	Provider  llm.Config
	Embedding embedding.Config
}

func Read(path string) (Values, error) {
	provider, err := llm.ConfigFromTOMLFile(path)
	if err != nil {
		return Values{}, err
	}
	embed, err := embedding.ConfigFromTOMLFile(path)
	return Values{Provider: provider, Embedding: embed}, err
}

func Write(path string, values Values) error {
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(".dogear", "config.toml")
	}
	var document map[string]any
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &document); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if document == nil {
		document = map[string]any{}
	}
	provider := map[string]any{"base_url": values.Provider.BaseURL, "model": values.Provider.Model, "api_key": values.Provider.APIKey}
	if values.Provider.Timeout > 0 {
		provider["timeout"] = values.Provider.Timeout.String()
	}
	document["provider"] = provider
	embed := map[string]any{
		"base_url": values.Embedding.BaseURL, "model": values.Embedding.Model, "api_key": values.Embedding.APIKey,
		"dimensions": values.Embedding.Dimensions, "batch_size": values.Embedding.BatchSize,
		"query_instruction": values.Embedding.QueryInstruction,
	}
	if values.Embedding.Timeout > 0 {
		embed["timeout"] = values.Embedding.Timeout.String()
	}
	document["embedding"] = embed
	for _, key := range []string{"base_url", "model", "api_key", "timeout"} {
		delete(document, key)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err := toml.NewEncoder(temp).Encode(document); err != nil {
		temp.Close()
		return fmt.Errorf("encode config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
