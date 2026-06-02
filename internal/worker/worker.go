package worker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/siqiliu18/healthwatch/internal/store"
)

type WorkerStore interface {
	ClaimJob(ctx context.Context) (*store.ClaimedJob, error)
	CompleteJob(ctx context.Context, jobID, checkID uuid.UUID, result store.CheckResultInput) error
	ReapStaleJobs(ctx context.Context, olderThan time.Duration) (int64, error)
}

type Worker struct {
	store       WorkerStore
	pingTimeout time.Duration
	concurrency int
}

func NewWorker(store WorkerStore, pingTimeout time.Duration, concurrency int) *Worker {
	return &Worker{store: store, pingTimeout: pingTimeout, concurrency: concurrency}
}

func (w *Worker) Run(ctx context.Context) {
	go w.runReaper(ctx)

	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runLoop(ctx)
		}()
	}
	wg.Wait()
}

func (w *Worker) runLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.processOne(ctx); err != nil {
			log.Printf("worker: processOne: %v", err)
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (w *Worker) processOne(ctx context.Context) error {
	job, err := w.store.ClaimJob(ctx)
	if err != nil {
		return err
	}
	if job == nil {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
		}
		return nil
	}
	result := ping(ctx, job.Endpoint, w.pingTimeout)
	return w.store.CompleteJob(ctx, job.JobID, job.CheckID, result)
}

func (w *Worker) runReaper(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			n, err := w.store.ReapStaleJobs(ctx, 2*w.pingTimeout)
			if err != nil {
				log.Printf("reaper: %v", err)
			} else if n > 0 {
				log.Printf("reaper: reset %d stale jobs", n)
			}
		case <-ctx.Done():
			return
		}
	}
}
