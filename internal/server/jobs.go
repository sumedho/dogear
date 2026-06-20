package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sumedho/dogear/internal/dogear"
)

type embeddingJobSnapshot struct {
	ID      string
	Indexed int
	Total   int
	Done    bool
	Result  dogear.EmbeddingIndexStatus
	Err     error
}

type embeddingJob struct {
	id      string
	indexed int
	total   int
	done    bool
	result  dogear.EmbeddingIndexStatus
	err     error
	changed chan struct{}
}

type embeddingJobManager struct {
	mu     sync.Mutex
	active *embeddingJob
}

func (m *embeddingJobManager) start(ctx context.Context, run func(context.Context, func(int, int)) (dogear.EmbeddingIndexStatus, error)) string {
	m.mu.Lock()
	if m.active != nil && !m.active.done {
		id := m.active.id
		m.mu.Unlock()
		return id
	}
	job := &embeddingJob{id: fmt.Sprintf("embed-%d", time.Now().UnixNano()), changed: make(chan struct{})}
	m.active = job
	m.mu.Unlock()

	go func() {
		result, err := run(ctx, func(indexed, total int) { m.update(job.id, indexed, total) })
		m.finish(job.id, result, err)
	}()
	return job.id
}

func (m *embeddingJobManager) update(id string, indexed, total int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.id != id || m.active.done {
		return
	}
	m.active.indexed = indexed
	m.active.total = total
	m.signalLocked()
}

func (m *embeddingJobManager) finish(id string, result dogear.EmbeddingIndexStatus, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.id != id || m.active.done {
		return
	}
	m.active.result = result
	m.active.err = err
	m.active.done = true
	m.signalLocked()
}

func (m *embeddingJobManager) snapshot(id string) (embeddingJobSnapshot, <-chan struct{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || (id != "" && m.active.id != id) {
		return embeddingJobSnapshot{}, nil, false
	}
	job := m.active
	return embeddingJobSnapshot{ID: job.id, Indexed: job.indexed, Total: job.total, Done: job.done, Result: job.result, Err: job.err}, job.changed, true
}

func (m *embeddingJobManager) signalLocked() {
	close(m.active.changed)
	m.active.changed = make(chan struct{})
}
