package llm

import (
	"context"
	"fmt"
	"os"
)

// Provider sends a single prompt (system + user) and returns the text response.
type Provider interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// NewFromEnv creates a Provider based on the LLM_PROVIDER env var.
// Defaults to "ollama" if LLM_PROVIDER is unset.
func NewFromEnv() (Provider, error) {
	switch p := os.Getenv("LLM_PROVIDER"); p {
	case "", "ollama":
		return newOllama(), nil
	case "claude":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=claude requires ANTHROPIC_API_KEY")
		}
		return newClaude(), nil
	case "openai":
		return nil, fmt.Errorf("LLM_PROVIDER=openai is not yet implemented; use 'ollama' (default) or 'claude'")
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q: use 'ollama' (default) or 'claude'", p)
	}
}
