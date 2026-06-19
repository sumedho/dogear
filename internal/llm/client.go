package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultBaseURL = "http://localhost:11434/v1"
	defaultTimeout = 60 * time.Second
)

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
}

type ChatResponse struct {
	Content string
}

type DryRun struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    ChatRequest       `json:"body"`
}

type Client struct {
	config Config
	http   *http.Client
}

func ConfigFromEnv() (Config, error) {
	timeout := defaultTimeout
	if value := strings.TrimSpace(os.Getenv("DOGEAR_TIMEOUT")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid DOGEAR_TIMEOUT: %w", err)
		}
		timeout = parsed
	}
	return Config{
		BaseURL: strings.TrimSpace(os.Getenv("DOGEAR_BASE_URL")),
		APIKey:  strings.TrimSpace(os.Getenv("DOGEAR_API_KEY")),
		Model:   strings.TrimSpace(os.Getenv("DOGEAR_MODEL")),
		Timeout: timeout,
	}, nil
}

func ConfigFromTOMLFile(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = filepath.Join(".dogear", "config.toml")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	return ParseConfigTOML(string(raw))
}

func ParseConfigTOML(content string) (Config, error) {
	var config Config
	section := ""
	for lineNo, line := range strings.Split(content, "\n") {
		line = stripTOMLComment(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		if section != "" && section != "provider" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("invalid TOML line %d: expected key = value", lineNo+1)
		}
		key = strings.TrimSpace(key)
		parsed, err := parseTOMLString(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("invalid TOML line %d: %w", lineNo+1, err)
		}
		switch key {
		case "base_url":
			config.BaseURL = parsed
		case "api_key":
			config.APIKey = parsed
		case "model":
			config.Model = parsed
		case "timeout":
			timeout, err := time.ParseDuration(parsed)
			if err != nil {
				return Config{}, fmt.Errorf("invalid TOML line %d timeout: %w", lineNo+1, err)
			}
			config.Timeout = timeout
		default:
			// Unknown keys are ignored so future config can be added without
			// breaking older binaries.
		}
	}
	return config, nil
}

func MergeConfig(base Config, override Config) Config {
	if override.BaseURL != "" {
		base.BaseURL = override.BaseURL
	}
	if override.APIKey != "" {
		base.APIKey = override.APIKey
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Timeout != 0 {
		base.Timeout = override.Timeout
	}
	return base
}

func stripTOMLComment(line string) string {
	inQuote := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inQuote {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if r == '#' && !inQuote {
			return line[:i]
		}
	}
	return line
}

func parseTOMLString(value string) (string, error) {
	if len(value) < 2 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
		return "", fmt.Errorf("expected quoted string")
	}
	var parsed string
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return "", err
	}
	return parsed, nil
}

func NewClient(config Config) (*Client, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("model is required; set DOGEAR_MODEL or pass --model")
	}
	endpoint, err := ChatCompletionsURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	config.BaseURL = endpoint
	return &Client{
		config: config,
		http:   &http.Client{Timeout: config.Timeout},
	}, nil
}

func ChatCompletionsURL(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = defaultBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid base URL %q", base)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/chat/completions") {
		parsed.Path = path
		return parsed.String(), nil
	}
	if path == "" {
		parsed.Path = "/v1/chat/completions"
		return parsed.String(), nil
	}
	parsed.Path = path + "/chat/completions"
	return parsed.String(), nil
}

func BuildRequest(model string, messages []Message) ChatRequest {
	return ChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.2,
	}
}

func (c *Client) DryRun(request ChatRequest) DryRun {
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if c.config.APIKey != "" {
		headers["Authorization"] = "Bearer <redacted>"
	}
	return DryRun{
		URL:     c.config.BaseURL,
		Headers: headers,
		Body:    request,
	}
}

func (c *Client) Chat(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ChatResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ChatResponse{}, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("malformed provider response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return ChatResponse{}, errors.New("provider response contained no choices")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return ChatResponse{}, errors.New("provider response contained empty content")
	}
	return ChatResponse{Content: content}, nil
}
