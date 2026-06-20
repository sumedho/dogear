package server

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sumedho/dogear/internal/dogear"
)

func TestEmbeddingJobManagerSharesActiveJob(t *testing.T) {
	var manager embeddingJobManager
	release := make(chan struct{})
	var runs atomic.Int32
	run := func(ctx context.Context, progress func(int, int)) (dogear.EmbeddingIndexStatus, error) {
		runs.Add(1)
		progress(1, 2)
		select {
		case <-release:
			return dogear.EmbeddingIndexStatus{Complete: true, Indexed: 2, Total: 2}, nil
		case <-ctx.Done():
			return dogear.EmbeddingIndexStatus{}, ctx.Err()
		}
	}
	first := manager.start(context.Background(), run)
	for {
		snapshot, changed, _ := manager.snapshot(first)
		if snapshot.Indexed == 1 {
			break
		}
		<-changed
	}
	second := manager.start(context.Background(), run)
	if first != second || runs.Load() != 1 {
		t.Fatalf("jobs first=%q second=%q runs=%d", first, second, runs.Load())
	}
	close(release)
	for {
		snapshot, changed, ok := manager.snapshot(first)
		if !ok {
			t.Fatal("active job disappeared")
		}
		if snapshot.Done {
			if !snapshot.Result.Complete {
				t.Fatalf("result = %#v", snapshot.Result)
			}
			break
		}
		<-changed
	}
}

func TestEmbeddingJobUsesLifecycleContext(t *testing.T) {
	var manager embeddingJobManager
	ctx, cancel := context.WithCancel(context.Background())
	id := manager.start(ctx, func(ctx context.Context, _ func(int, int)) (dogear.EmbeddingIndexStatus, error) {
		<-ctx.Done()
		return dogear.EmbeddingIndexStatus{}, ctx.Err()
	})
	cancel()
	for {
		snapshot, changed, _ := manager.snapshot(id)
		if snapshot.Done {
			if snapshot.Err != context.Canceled {
				t.Fatalf("error = %v", snapshot.Err)
			}
			break
		}
		<-changed
	}
}
