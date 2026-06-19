package llm

import (
	"bufio"
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

	"github.com/BurntSushi/toml"
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
	Stream      bool      `json:"stream,omitempty"`
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
	type providerConfig struct {
		BaseURL *string `toml:"base_url"`
		APIKey  *string `toml:"api_key"`
		Model   *string `toml:"model"`
		Timeout *string `toml:"timeout"`
	}
	type configFile struct {
		BaseURL  *string        `toml:"base_url"`
		APIKey   *string        `toml:"api_key"`
		Model    *string        `toml:"model"`
		Timeout  *string        `toml:"timeout"`
		Provider providerConfig `toml:"provider"`
	}

	var decoded configFile
	if _, err := toml.Decode(content, &decoded); err != nil {
		return Config{}, fmt.Errorf("invalid TOML: %w", err)
	}

	config := Config{}
	apply := func(baseURL, apiKey, model, timeoutValue *string) error {
		if baseURL != nil {
			config.BaseURL = *baseURL
		}
		if apiKey != nil {
			config.APIKey = *apiKey
		}
		if model != nil {
			config.Model = *model
		}
		if timeoutValue != nil {
			timeout, err := time.ParseDuration(*timeoutValue)
			if err != nil {
				return fmt.Errorf("invalid timeout: %w", err)
			}
			config.Timeout = timeout
		}
		return nil
	}

	if err := apply(decoded.BaseURL, decoded.APIKey, decoded.Model, decoded.Timeout); err != nil {
		return Config{}, err
	}
	if err := apply(decoded.Provider.BaseURL, decoded.Provider.APIKey, decoded.Provider.Model, decoded.Provider.Timeout); err != nil {
		return Config{}, err
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
	resp, err := c.doChatRequest(ctx, request)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ChatResponse{}, err
	}
	if err := providerStatusError(resp.StatusCode, body); err != nil {
		return ChatResponse{}, err
	}
	return parseChatResponse(body)
}

func (c *Client) ChatStream(ctx context.Context, request ChatRequest, onDelta func(string) error) (ChatResponse, error) {
	request.Stream = true
	resp, err := c.doChatRequest(ctx, request)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return ChatResponse{}, err
		}
		if err := providerStatusError(resp.StatusCode, body); err != nil {
			return ChatResponse{}, err
		}
		result, err := parseChatResponse(body)
		if err != nil {
			return ChatResponse{}, err
		}
		if onDelta != nil {
			if err := onDelta(result.Content); err != nil {
				return ChatResponse{}, err
			}
		}
		return result, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return ChatResponse{}, providerStatusError(resp.StatusCode, body)
	}

	var content strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var event struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return ChatResponse{}, fmt.Errorf("malformed provider stream: %w", err)
		}
		if event.Error != nil {
			return ChatResponse{}, fmt.Errorf("provider stream error: %s", event.Error.Message)
		}
		for _, choice := range event.Choices {
			delta := choice.Delta.Content
			if delta == "" {
				delta = choice.Message.Content
			}
			if delta == "" {
				continue
			}
			content.WriteString(delta)
			if onDelta != nil {
				if err := onDelta(delta); err != nil {
					return ChatResponse{}, err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ChatResponse{}, err
	}
	answer := strings.TrimSpace(content.String())
	if answer == "" {
		return ChatResponse{}, errors.New("provider response contained empty content")
	}
	return ChatResponse{Content: answer}, nil
}

func (c *Client) doChatRequest(ctx context.Context, request ChatRequest) (*http.Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if request.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func parseChatResponse(respBody []byte) (ChatResponse, error) {
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

func providerStatusError(status int, body []byte) error {
	if status >= 200 && status <= 299 {
		return nil
	}
	return fmt.Errorf("provider returned HTTP %d: %s", status, strings.TrimSpace(string(body)))
}
