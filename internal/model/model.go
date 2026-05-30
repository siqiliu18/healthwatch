package model

import (
	"time"

	"github.com/google/uuid"
)

type Check struct {
	ID        uuid.UUID `json:"id"`
	Endpoint  string    `json:"endpoint"`
	CreatedAt time.Time `json:"created_at"`
}

type CheckJob struct {
	ID          uuid.UUID  `json:"id"`
	CheckID     uuid.UUID  `json:"check_id"`
	Status      string     `json:"status"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	DoneAt      *time.Time `json:"done_at,omitempty"`
}

type CheckResult struct {
	ID         uuid.UUID  `json:"id"`
	CheckID    uuid.UUID  `json:"check_id"`
	JobID      *uuid.UUID `json:"job_id,omitempty"`
	StatusCode *int       `json:"status_code,omitempty"`
	StatusText *string    `json:"status_text,omitempty"`
	DurationMs *int       `json:"duration_ms,omitempty"`
	Error      *string    `json:"error,omitempty"`
	CheckedAt  time.Time  `json:"checked_at"`
}
