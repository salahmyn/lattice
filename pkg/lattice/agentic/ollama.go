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

// OllamaProvider calls a local Ollama server.
type OllamaProvider struct {
	Model   string
	BaseURL string
	Timeout time.Duration
}

// Name returns the provider name.
func (p *OllamaProvider) Name() string { return "ollama" }

// Complete sends a non-streaming chat request to Ollama.
func (p *OllamaProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	base := p.BaseURL
	if base == "" {
		base = "http://localhost:11434"
	}
	messages := []map[string]string{}
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.UserMessage})

	body := map[string]interface{}{"model": p.Model, "messages": messages, "stream": false}
	payload, _ := json.Marshal(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: p.Timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("ollama API %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		PromptEvalCount int `json:"prompt_eval_count"`
		EvalCount       int `json:"eval_count"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CompletionResponse{}, err
	}
	return CompletionResponse{
		Text:       parsed.Message.Content,
		TokensUsed: parsed.PromptEvalCount + parsed.EvalCount,
	}, nil
}
