package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/influx"
)

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
