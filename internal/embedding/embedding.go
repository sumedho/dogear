package embedding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const defaultInstruction = "Retrieve relevant passages from product manuals that answer the user's question."

type Config struct {
	BaseURL          string
	APIKey           string
	Model            string
	Dimensions       int
	BatchSize        int
	QueryInstruction string
	Timeout          time.Duration
}

type fileConfig struct {
	Embedding struct {
		BaseURL          string `toml:"base_url"`
		APIKey           string `toml:"api_key"`
		Model            string `toml:"model"`
		Dimensions       int    `toml:"dimensions"`
		BatchSize        int    `toml:"batch_size"`
		QueryInstruction string `toml:"query_instruction"`
		Timeout          string `toml:"timeout"`
	} `toml:"embedding"`
}

func ConfigFromTOMLFile(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(".dogear", "config.toml")
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var decoded fileConfig
	if _, err := toml.Decode(string(raw), &decoded); err != nil {
		return Config{}, fmt.Errorf("invalid TOML: %w", err)
	}
	config := Config{
		BaseURL: strings.TrimSpace(decoded.Embedding.BaseURL), APIKey: strings.TrimSpace(decoded.Embedding.APIKey),
		Model: strings.TrimSpace(decoded.Embedding.Model), Dimensions: decoded.Embedding.Dimensions,
		BatchSize: decoded.Embedding.BatchSize, QueryInstruction: strings.TrimSpace(decoded.Embedding.QueryInstruction),
	}
	if decoded.Embedding.Timeout != "" {
		config.Timeout, err = time.ParseDuration(decoded.Embedding.Timeout)
		if err != nil {
			return Config{}, fmt.Errorf("invalid embedding timeout: %w", err)
		}
	}
	return config, nil
}

func ConfigFromEnv() (Config, error) {
	config := Config{
		BaseURL:          strings.TrimSpace(os.Getenv("DOGEAR_EMBEDDING_BASE_URL")),
		APIKey:           strings.TrimSpace(os.Getenv("DOGEAR_EMBEDDING_API_KEY")),
		Model:            strings.TrimSpace(os.Getenv("DOGEAR_EMBEDDING_MODEL")),
		QueryInstruction: strings.TrimSpace(os.Getenv("DOGEAR_EMBEDDING_QUERY_INSTRUCTION")),
	}
	var err error
	if value := strings.TrimSpace(os.Getenv("DOGEAR_EMBEDDING_DIMENSIONS")); value != "" {
		config.Dimensions, err = strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid DOGEAR_EMBEDDING_DIMENSIONS: %w", err)
		}
	}
	if value := strings.TrimSpace(os.Getenv("DOGEAR_EMBEDDING_BATCH_SIZE")); value != "" {
		config.BatchSize, err = strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid DOGEAR_EMBEDDING_BATCH_SIZE: %w", err)
		}
	}
	if value := strings.TrimSpace(os.Getenv("DOGEAR_EMBEDDING_TIMEOUT")); value != "" {
		config.Timeout, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid DOGEAR_EMBEDDING_TIMEOUT: %w", err)
		}
	}
	return config, nil
}

func Merge(base, override Config) Config {
	if override.BaseURL != "" {
		base.BaseURL = override.BaseURL
	}
	if override.APIKey != "" {
		base.APIKey = override.APIKey
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Dimensions != 0 {
		base.Dimensions = override.Dimensions
	}
	if override.BatchSize != 0 {
		base.BatchSize = override.BatchSize
	}
	if override.QueryInstruction != "" {
		base.QueryInstruction = override.QueryInstruction
	}
	if override.Timeout != 0 {
		base.Timeout = override.Timeout
	}
	return base
}

func Resolve(path string, providerBaseURL, providerAPIKey string) (Config, error) {
	file, err := ConfigFromTOMLFile(path)
	if err != nil {
		return Config{}, err
	}
	env, err := ConfigFromEnv()
	if err != nil {
		return Config{}, err
	}
	config := Merge(file, env)
	if config.Dimensions == 0 {
		config.Dimensions = 1024
	}
	if config.BatchSize == 0 {
		config.BatchSize = 16
	}
	if config.QueryInstruction == "" {
		config.QueryInstruction = defaultInstruction
	}
	if config.Timeout == 0 {
		config.Timeout = 120 * time.Second
	}
	if config.APIKey == "" && sameOrigin(config.BaseURL, providerBaseURL) {
		config.APIKey = providerAPIKey
	}
	if config.Dimensions < 32 || config.Dimensions > 4096 {
		return Config{}, fmt.Errorf("embedding dimensions must be between 32 and 4096")
	}
	if config.BatchSize < 1 || config.BatchSize > 256 {
		return Config{}, fmt.Errorf("embedding batch size must be between 1 and 256")
	}
	return config, nil
}

func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Scheme != "" && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

type Client struct {
	config   Config
	endpoint string
	http     *http.Client
}

func NewClient(config Config) (*Client, error) {
	if config.Model == "" {
		return nil, errors.New("embedding model is not configured")
	}
	endpoint, err := EmbeddingsURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	return &Client{config: config, endpoint: endpoint, http: &http.Client{Timeout: config.Timeout}}, nil
}

func EmbeddingsURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid embedding base URL %q", base)
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/embeddings") {
		path += "/embeddings"
	}
	if path == "/embeddings" {
		path = "/v1/embeddings"
	}
	u.Path = path
	return u.String(), nil
}

func (c *Client) Embed(ctx context.Context, input []string) ([][]float32, error) {
	if len(input) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"model": c.config.Model, "input": input, "dimensions": c.config.Dimensions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("embedding provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("malformed embedding response: %w", err)
	}
	if len(parsed.Data) != len(input) {
		return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(parsed.Data), len(input))
	}
	out := make([][]float32, len(input))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(out) || len(item.Embedding) != c.config.Dimensions {
			return nil, fmt.Errorf("invalid embedding response index or dimensions")
		}
		out[item.Index] = item.Embedding
	}
	return out, nil
}

func (c *Client) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	vectors, err := c.Embed(ctx, []string{"Instruct: " + c.config.QueryInstruction + "\nQuery: " + query})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (c *Client) Config() Config { return c.config }

func (c Config) IndexHash() string {
	value := fmt.Sprintf("%s\x00%d\x00%s\x00v1", c.Model, c.Dimensions, c.QueryInstruction)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}
