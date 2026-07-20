package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func claudeTestProvider(t *testing.T, handler http.HandlerFunc) *claudeProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := anthropic.NewClient(
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
	return &claudeProvider{client: &c, model: "claude-test"}
}

func anthropicResp(text string) map[string]any {
	return map[string]any{
		"id":   "msg_test",
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"model":       "claude-test",
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
	}
}

func TestClaudeComplete_success(t *testing.T) {
	p := claudeTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anthropicResp("great training week"))
	})

	got, err := p.Complete(context.Background(), "you are a coach", "analyze my week")
	if err != nil {
		t.Fatal(err)
	}
	if got != "great training week" {
		t.Errorf("got %q, want %q", got, "great training week")
	}
}

func TestClaudeComplete_systemPromptSent(t *testing.T) {
	var body map[string]any
	p := claudeTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anthropicResp("ok"))
	})

	if _, err := p.Complete(context.Background(), "you are a coach", "hello"); err != nil {
		t.Fatal(err)
	}
	sys, ok := body["system"]
	if !ok {
		t.Fatal("system field missing from request body")
	}
	arr, ok := sys.([]any)
	if !ok || len(arr) == 0 {
		t.Errorf("system should be non-empty array, got %v", sys)
	}
}

func TestClaudeComplete_noSystemPrompt(t *testing.T) {
	var body map[string]any
	p := claudeTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anthropicResp("ok"))
	})

	if _, err := p.Complete(context.Background(), "", "hello"); err != nil {
		t.Fatal(err)
	}
	// system should be absent or empty when no system prompt is provided
	if sys, ok := body["system"]; ok {
		if arr, ok := sys.([]any); ok && len(arr) > 0 {
			t.Errorf("system sent when none provided: %v", sys)
		}
	}
}

func TestClaudeComplete_emptyResponse(t *testing.T) {
	p := claudeTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_test",
			"type":        "message",
			"role":        "assistant",
			"content":     []any{},
			"model":       "claude-test",
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 0},
		})
	})

	_, err := p.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("error should mention 'empty response': %v", err)
	}
}

func TestClaudeComplete_httpError(t *testing.T) {
	p := claudeTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`))
	})

	_, err := p.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "claude:") {
		t.Errorf("error should be wrapped with 'claude:': %v", err)
	}
}

func TestClaudeComplete_contextCancelled(t *testing.T) {
	p := claudeTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Complete(ctx, "", "hello")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
