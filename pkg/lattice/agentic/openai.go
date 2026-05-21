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

// OpenAIProvider calls an OpenAI-compatible chat-completions endpoint.
type OpenAIProvider struct {
	APIKey    string
	Model     string
	BaseURL   string
	Timeout   time.Duration
	MaxTokens int
}

// Name returns the provider name.
func (p *OpenAIProvider) Name() string { return "openai" }

// Complete sends a chat-completion request.
func (p *OpenAIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	base := p.BaseURL
	if base == "" {
		base = "https://api.openai.com"
	}
	messages := []map[string]string{}
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.UserMessage})

	body := map[string]interface{}{"model": p.Model, "messages": messages}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	} else if p.MaxTokens > 0 {
		body["max_tokens"] = p.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	payload, _ := json.Marshal(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+p.APIKey)

	client := &http.Client{Timeout: p.Timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("openai API %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CompletionResponse{}, err
	}
	if len(parsed.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("openai API: empty response")
	}
	return CompletionResponse{Text: parsed.Choices[0].Message.Content, TokensUsed: parsed.Usage.TotalTokens}, nil
}
