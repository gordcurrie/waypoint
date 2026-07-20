package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ollamaTestServer(t *testing.T, handler http.HandlerFunc) (*ollamaProvider, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	p := &ollamaProvider{
		url:    srv.URL,
		model:  "llama3.2",
		client: &http.Client{},
	}
	return p, srv.Close
}

func TestOllamaComplete_success(t *testing.T) {
	p, close := ollamaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "llama3.2" {
			t.Errorf("model: got %q, want %q", req.Model, "llama3.2")
		}
		if req.Stream {
			t.Error("stream should be false")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{
			Message: ollamaMessage{Role: "assistant", Content: "great week"},
		})
	})
	defer close()

	got, err := p.Complete(context.Background(), "", "how was my training?")
	if err != nil {
		t.Fatal(err)
	}
	if got != "great week" {
		t.Errorf("got %q, want %q", got, "great week")
	}
}

func TestOllamaComplete_systemPromptIncluded(t *testing.T) {
	p, close := ollamaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 2 {
			t.Fatalf("expected 2 messages (system+user), got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("first message role: got %q, want %q", req.Messages[0].Role, "system")
		}
		if req.Messages[1].Role != "user" {
			t.Errorf("second message role: got %q, want %q", req.Messages[1].Role, "user")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{
			Message: ollamaMessage{Role: "assistant", Content: "ok"},
		})
	})
	defer close()

	if _, err := p.Complete(context.Background(), "you are a coach", "analyze my week"); err != nil {
		t.Fatal(err)
	}
}

func TestOllamaComplete_noSystemPrompt(t *testing.T) {
	p, close := ollamaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("expected 1 message (user only), got %d", len(req.Messages))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{
			Message: ollamaMessage{Role: "assistant", Content: "ok"},
		})
	})
	defer close()

	if _, err := p.Complete(context.Background(), "", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestOllamaComplete_emptyResponse(t *testing.T) {
	p, close := ollamaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{
			Message: ollamaMessage{Role: "assistant", Content: ""},
		})
	})
	defer close()

	_, err := p.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("error should mention 'empty response': %v", err)
	}
}

func TestOllamaComplete_httpError(t *testing.T) {
	p, close := ollamaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	})
	defer close()

	_, err := p.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for HTTP 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should include status code: %v", err)
	}
}

func TestOllamaComplete_invalidJSON(t *testing.T) {
	p, close := ollamaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	})
	defer close()

	_, err := p.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error should mention decode: %v", err)
	}
}

func TestOllamaComplete_contextCancelled(t *testing.T) {
	p, close := ollamaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// never respond — context should cancel first
		<-r.Context().Done()
	})
	defer close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Complete(ctx, "", "hello")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
