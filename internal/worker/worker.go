package worker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/siqiliu18/healthwatch/internal/model"
	"github.com/siqiliu18/healthwatch/internal/store"
)

type WorkerStore interface {
	ClaimJob(ctx context.Context) (*store.ClaimedJob, error)
	CompleteJob(ctx context.Context, jobID, checkID uuid.UUID, result store.CheckResultInput) (*model.CheckResult, error)
	ReapStaleJobs(ctx context.Context, olderThan time.Duration) (int64, error)
}

type Worker struct {
	store       WorkerStore
	cache       store.Cache // nil if Redis is not configured
	pingTimeout time.Duration
	concurrency int
}

func NewWorker(s WorkerStore, cache store.Cache, pingTimeout time.Duration, concurrency int) *Worker {
	return &Worker{store: s, cache: cache, pingTimeout: pingTimeout, concurrency: concurrency}
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
	pingResult := ping(ctx, job.Endpoint, w.pingTimeout)
	saved, err := w.store.CompleteJob(ctx, job.JobID, job.CheckID, pingResult)
	if err != nil {
		return err
	}
	if w.cache != nil {
		if err := w.cache.SetLatestResult(ctx, job.CheckID, saved); err != nil {
			log.Printf("worker: cache set: %v", err)
		}
	}
	return nil
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
