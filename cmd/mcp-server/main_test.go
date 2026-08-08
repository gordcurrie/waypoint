package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/influx"
)

// syncBuffer is a bytes.Buffer safe for concurrent writes (from the background loop's
// slog calls) and reads (from the test goroutine polling for output).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("syncBuffer: %w", err)
	}
	return n, nil
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

type fakeTrainingLoadClient struct {
	queries atomic.Int32
	writes  atomic.Int32
}

func (f *fakeTrainingLoadClient) Query(_ context.Context, _ string) ([]map[string]any, error) {
	f.queries.Add(1)
	return nil, nil
}

func (f *fakeTrainingLoadClient) WritePoints(_ context.Context, _ ...*influx.Point) error {
	f.writes.Add(1)
	return nil
}

func TestRunTrainingLoadLoop_ComputesImmediatelyAndOnTick(t *testing.T) {
	client := &fakeTrainingLoadClient{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runTrainingLoadLoop(ctx, client, 10*time.Millisecond)
		close(done)
	}()

	// Wait for the immediate compute plus at least one tick-driven compute.
	deadline := time.Now().Add(time.Second)
	for client.queries.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for 2 computes, got %d", client.queries.Load())
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not exit after context cancellation")
	}

	if client.writes.Load() == 0 {
		t.Error("want WritePoints called at least once")
	}
}

// erroringClient always fails Query with the given error, so WritePoints is never reached.
type erroringClient struct {
	err error
}

func (e *erroringClient) Query(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, e.err
}

func (e *erroringClient) WritePoints(_ context.Context, _ ...*influx.Point) error {
	return nil
}

func TestRunTrainingLoadLoop_SuppressesContextCanceledLogging(t *testing.T) {
	var buf syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled — compute() must run at least once via the immediate call

	done := make(chan struct{})
	go func() {
		runTrainingLoadLoop(ctx, &erroringClient{err: context.Canceled}, time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not exit for an already-canceled context")
	}

	if strings.Contains(buf.String(), "background compute failed") {
		t.Errorf("want context.Canceled suppressed, got log output: %s", buf.String())
	}
}

func TestRunTrainingLoadLoop_LogsNonCancellationErrors(t *testing.T) {
	var buf syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runTrainingLoadLoop(ctx, &erroringClient{err: errors.New("influx unreachable")}, time.Hour)

	deadline := time.Now().Add(time.Second)
	for !strings.Contains(buf.String(), "background compute failed") {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a real error to be logged, got: %s", buf.String())
		}
		time.Sleep(time.Millisecond)
	}
}
