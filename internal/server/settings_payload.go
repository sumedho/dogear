package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/sumedho/dogear/internal/embedding"
	"github.com/sumedho/dogear/internal/llm"
)

func applyProviderPayload(config *llm.Config, payload settingsProviderPayload, timeoutRequired bool) error {
	config.BaseURL = strings.TrimSpace(payload.BaseURL)
	config.Model = strings.TrimSpace(payload.Model)
	if payload.Timeout == "" {
		if timeoutRequired {
			return fmt.Errorf("provider timeout is required")
		}
	} else {
		timeout, err := time.ParseDuration(payload.Timeout)
		if err != nil {
			return fmt.Errorf("invalid provider timeout: %w", err)
		}
		config.Timeout = timeout
	}
	return applyKeyAction(&config.APIKey, payload.APIKeyAction, payload.APIKey)
}

func applyEmbeddingPayload(config *embedding.Config, payload settingsEmbeddingPayload, timeoutRequired bool) error {
	config.BaseURL = strings.TrimSpace(payload.BaseURL)
	config.Model = strings.TrimSpace(payload.Model)
	config.QueryInstruction = strings.TrimSpace(payload.QueryInstruction)
	if payload.Dimensions < 32 || payload.Dimensions > 4096 {
		return fmt.Errorf("embedding dimensions must be between 32 and 4096")
	}
	if payload.BatchSize < 1 || payload.BatchSize > 256 {
		return fmt.Errorf("embedding batch size must be between 1 and 256")
	}
	config.Dimensions = payload.Dimensions
	config.BatchSize = payload.BatchSize
	if payload.Timeout == "" {
		if timeoutRequired {
			return fmt.Errorf("embedding timeout is required")
		}
	} else {
		timeout, err := time.ParseDuration(payload.Timeout)
		if err != nil {
			return fmt.Errorf("invalid embedding timeout: %w", err)
		}
		config.Timeout = timeout
	}
	return applyKeyAction(&config.APIKey, payload.APIKeyAction, payload.APIKey)
}
