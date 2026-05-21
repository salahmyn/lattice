package agentic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AnthropicProvider calls the Anthropic Messages API.
type AnthropicProvider struct {
	APIKey    string
	Model     string
	BaseURL   string
	Timeout   time.Duration
	MaxTokens int
}

// Name returns the provider name.
func (p *AnthropicProvider) Name() string { return "anthropic" }

// Complete sends a single-turn request to the Anthropic Messages API.
func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	base := p.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = 2000
	}

	body := map[string]interface{}{
		"model":      p.Model,
		"max_tokens": maxTokens,
		"messages":   []map[string]string{{"role": "user", "content": req.UserMessage}},
	}
	if req.SystemPrompt != "" {
		body["system"] = req.SystemPrompt
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	payload, _ := json.Marshal(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: p.Timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("anthropic API %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CompletionResponse{}, err
	}
	text := ""
	for _, c := range parsed.Content {
		text += c.Text
	}
	return CompletionResponse{Text: text, TokensUsed: parsed.Usage.InputTokens + parsed.Usage.OutputTokens}, nil
}
