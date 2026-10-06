package queue

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"runnerd/internal/model"
)

var ErrFull = errors.New("runner queue full")
var ErrStopped = errors.New("runner queue stopped")

type Executor interface {
	Run(context.Context, model.RunnerRequest) error
}

type Queue struct {
	mu      sync.Mutex
	stopped bool
	jobs    chan model.RunnerRequest
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(parent context.Context, size, workers int, executor Executor, log *slog.Logger) *Queue {
	ctx, cancel := context.WithCancel(parent)
	q := &Queue{jobs: make(chan model.RunnerRequest, size), cancel: cancel}
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case req := <-q.jobs:
					if ctx.Err() != nil {
						return
					}
					if err := executor.Run(ctx, req); err != nil {
						log.Error("runner request failed", "delivery_id", req.DeliveryID, "repository", req.FullName, "workflow_job_id", req.JobID, "error", err)
					}
				}
			}
		}()
	}
	return q
}

func (q *Queue) Enqueue(req model.RunnerRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return ErrStopped
	}
	select {
	case q.jobs <- req:
		return nil
	default:
		return ErrFull
	}
}

func (q *Queue) Stop() { q.mu.Lock(); q.stopped = true; q.cancel(); q.mu.Unlock() }
func (q *Queue) Wait() { q.wg.Wait() }
