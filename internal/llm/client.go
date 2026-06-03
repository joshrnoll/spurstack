package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultTimeout          = 10 * time.Minute
	DefaultMaxResponseBytes = 4 * 1024 * 1024
)

type Client struct {
	apiKey           string
	baseURL          string
	model            string
	providerOrder    []string
	allowFallbacks   bool
	http             *http.Client
	timeout          time.Duration
	maxResponseBytes int64
}

func New(apiKey, baseURL, model string) *Client {
	return NewWithProvider(apiKey, baseURL, model, nil, true)
}

func NewWithProvider(apiKey, baseURL, model string, providerOrder []string, allowFallbacks bool) *Client {
	return NewWithProviderAndLimits(apiKey, baseURL, model, providerOrder, allowFallbacks, DefaultTimeout, DefaultMaxResponseBytes)
}

func NewWithProviderAndLimits(apiKey, baseURL, model string, providerOrder []string, allowFallbacks bool, timeout time.Duration, maxResponseBytes int64) *Client {
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	return &Client{
		apiKey:           apiKey,
		baseURL:          strings.TrimRight(baseURL, "/"),
		model:            model,
		providerOrder:    providerOrder,
		allowFallbacks:   allowFallbacks,
		http:             &http.Client{Timeout: timeout},
		timeout:          timeout,
		maxResponseBytes: maxResponseBytes,
	}
}

type Message struct{ Role, Content string }

type chatReq struct {
	Model       string        `json:"model"`
	Messages    []chatMsg     `json:"messages"`
	Temperature float64       `json:"temperature"`
	Provider    *providerPref `json:"provider,omitempty"`
}

type providerPref struct {
	Order          []string `json:"order,omitempty"`
	AllowFallbacks bool     `json:"allow_fallbacks"`
}
type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type chatResp struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > maxBytes {
		return body[:maxBytes], true, nil
	}
	return body, false, nil
}

func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	reqBody := chatReq{Model: c.model, Temperature: 0.2}
	if len(c.providerOrder) > 0 {
		reqBody.Provider = &providerPref{Order: c.providerOrder, AllowFallbacks: c.allowFallbacks}
	}
	for _, m := range messages {
		reqBody.Messages = append(reqBody.Messages, chatMsg{Role: m.Role, Content: m.Content})
	}
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, oversized, _ := readLimited(resp.Body, c.maxResponseBytes)
		if oversized {
			return "", fmt.Errorf("llm request failed: %s: response exceeded %d bytes", resp.Status, c.maxResponseBytes)
		}
		return "", fmt.Errorf("llm request failed: %s: %s", resp.Status, string(body))
	}
	body, oversized, err := readLimited(resp.Body, c.maxResponseBytes)
	if err != nil {
		return "", err
	}
	if oversized {
		return "", fmt.Errorf("llm response exceeded %d bytes", c.maxResponseBytes)
	}
	var cr chatResp
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return cr.Choices[0].Message.Content, nil
}
