package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

type SchedulerStore interface {
	ListAllCheckIDs(ctx context.Context) ([]uuid.UUID, error)
	EnqueueJobs(ctx context.Context, checkIDs []uuid.UUID) error
}

type Scheduler struct {
	store     SchedulerStore
	frequency time.Duration
}

func NewScheduler(store SchedulerStore, frequency time.Duration) *Scheduler {
	return &Scheduler{store: store, frequency: frequency}
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.frequency)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.enqueue(ctx); err != nil {
				log.Printf("scheduler: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scheduler) enqueue(ctx context.Context) error {
	ids, err := s.store.ListAllCheckIDs(ctx)
	if err != nil {
		return fmt.Errorf("list checks: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.store.EnqueueJobs(ctx, ids); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	log.Printf("scheduler: enqueued %d jobs", len(ids))
	return nil
}
