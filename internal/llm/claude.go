package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

const defaultClaudeModel = "claude-sonnet-4-6"

type claudeProvider struct {
	client *anthropic.Client
	model  anthropic.Model
}

func newClaude() *claudeProvider {
	model := os.Getenv("CLAUDE_MODEL")
	if model == "" {
		model = defaultClaudeModel
	}
	c := anthropic.NewClient() // reads ANTHROPIC_API_KEY
	return &claudeProvider{
		client: &c,
		model:  anthropic.Model(model),
	}
}

// Complete sends a system+user prompt to Claude and returns the text response.
func (p *claudeProvider) Complete(ctx context.Context, system, user string) (string, error) {
	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	if string(resp.StopReason) == "max_tokens" {
		return "", fmt.Errorf("claude: response truncated at max_tokens=%d; reduce input or increase MaxTokens", params.MaxTokens)
	}

	var sb strings.Builder
	for i := range resp.Content {
		if resp.Content[i].Type == "text" {
			sb.WriteString(resp.Content[i].AsText().Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("claude: empty response")
	}
	return sb.String(), nil
}
