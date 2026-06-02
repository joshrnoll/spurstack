package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Client struct {
	apiKey         string
	baseURL        string
	model          string
	providerOrder  []string
	allowFallbacks bool
	http           *http.Client
}

func New(apiKey, baseURL, model string) *Client {
	return NewWithProvider(apiKey, baseURL, model, nil, true)
}

func NewWithProvider(apiKey, baseURL, model string, providerOrder []string, allowFallbacks bool) *Client {
	return &Client{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), model: model, providerOrder: providerOrder, allowFallbacks: allowFallbacks, http: http.DefaultClient}
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

func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
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
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return "", fmt.Errorf("llm request failed: %s: %s", resp.Status, buf.String())
	}
	var cr chatResp
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return cr.Choices[0].Message.Content, nil
}
