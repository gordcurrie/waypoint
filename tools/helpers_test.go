package tools

import (
	"errors"
	"testing"
)

func TestTextResult(t *testing.T) {
	result, _, err := textResult("hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("textResult must not be an error")
	}
	if len(result.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(result.Content))
	}
}

func TestErrorResult(t *testing.T) {
	result, _, err := errorResult(errors.New("boom"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("errorResult must set IsError")
	}
}

func TestJSONResult(t *testing.T) {
	result, _, err := jsonResult(map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("jsonResult must not be an error")
	}
	if len(result.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(result.Content))
	}
}

func TestJSONResult_UnmarshalableInput(t *testing.T) {
	result, _, err := jsonResult(make(chan int))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("jsonResult with unmarshalable input must return IsError:true")
	}
}
