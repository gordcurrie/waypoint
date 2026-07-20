package llm

import (
	"strings"
	"testing"
)

func TestNewFromEnv_defaultOllama(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")

	p, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*ollamaProvider); !ok {
		t.Errorf("expected *ollamaProvider, got %T", p)
	}
}

func TestNewFromEnv_explicitOllama(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "ollama")

	p, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*ollamaProvider); !ok {
		t.Errorf("expected *ollamaProvider, got %T", p)
	}
}

func TestNewFromEnv_ollamaReadsEnv(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "ollama")
	t.Setenv("OLLAMA_BASE_URL", "http://myhost:11434")
	t.Setenv("OLLAMA_MODEL", "mistral")

	p, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	op, ok := p.(*ollamaProvider)
	if !ok {
		t.Fatalf("expected *ollamaProvider, got %T", p)
	}
	if op.url != "http://myhost:11434" {
		t.Errorf("url: got %q, want %q", op.url, "http://myhost:11434")
	}
	if op.model != "mistral" {
		t.Errorf("model: got %q, want %q", op.model, "mistral")
	}
}

func TestNewFromEnv_claude(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "claude")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	p, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*claudeProvider); !ok {
		t.Errorf("expected *claudeProvider, got %T", p)
	}
}

func TestNewFromEnv_claudeMissingKey(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "claude")
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("error should mention ANTHROPIC_API_KEY: %v", err)
	}
}

func TestNewFromEnv_unknownProvider(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "gpt4")

	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "gpt4") {
		t.Errorf("error should mention unknown provider name: %v", err)
	}
}

func TestNewFromEnv_openaiNotImplemented(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")

	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error for openai provider, got nil")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error should mention 'not yet implemented': %v", err)
	}
}

func TestNewFromEnv_claudeReadsModel(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "claude")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("CLAUDE_MODEL", "claude-opus-4-8")

	p, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cp, ok := p.(*claudeProvider)
	if !ok {
		t.Fatalf("expected *claudeProvider, got %T", p)
	}
	if string(cp.model) != "claude-opus-4-8" {
		t.Errorf("model: got %q, want %q", cp.model, "claude-opus-4-8")
	}
}
