package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const defaultOllamaURL = "http://localhost:11434"
const defaultOllamaModel = "llama3.2"

type ollamaProvider struct {
	url    string
	model  string
	client *http.Client
}

func newOllama() *ollamaProvider {
	url := os.Getenv("OLLAMA_BASE_URL")
	if url == "" {
		url = defaultOllamaURL
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = defaultOllamaModel
	}
	return &ollamaProvider{
		url:   url,
		model: model,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
}

// Complete sends a system+user prompt to Ollama and returns the text response.
func (p *ollamaProvider) Complete(ctx context.Context, system, user string) (string, error) {
	messages := make([]ollamaMessage, 0, 2)
	if system != "" {
		messages = append(messages, ollamaMessage{Role: "system", Content: system})
	}
	messages = append(messages, ollamaMessage{Role: "user", Content: user})

	body, err := json.Marshal(ollamaChatRequest{
		Model:    p.model,
		Messages: messages,
		Stream:   false,
	})
	if err != nil {
		return "", fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req) //nolint:gosec // URL comes from OLLAMA_BASE_URL env var, operator-controlled
	if err != nil {
		return "", fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ollama: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, string(data))
	}

	var result ollamaChatResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("ollama: decode response: %w", err)
	}
	return result.Message.Content, nil
}
