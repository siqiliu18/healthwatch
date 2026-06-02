package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siqiliu18/healthwatch/internal/model"
)

var ErrNotFound = errors.New("not found")

type ClaimedJob struct {
	JobID    uuid.UUID
	CheckID  uuid.UUID
	Endpoint string
}

type CheckResultInput struct {
	StatusCode *int
	StatusText *string
	DurationMs *int
	Error      *string
}

type Store interface {
	Register(ctx context.Context, endpoint string) (*model.Check, error)
	GetCheck(ctx context.Context, id uuid.UUID) (*model.Check, error)
	GetLatestResult(ctx context.Context, checkID uuid.UUID) (*model.CheckResult, error)
	DeleteCheck(ctx context.Context, id uuid.UUID) error
	ListChecks(ctx context.Context, limit, offset int) ([]*model.Check, error)
	EnqueueJob(ctx context.Context, checkID uuid.UUID) error
}

// SchedulerStore is the subset of PostgresStore used by the scheduler goroutine.
type SchedulerStore interface {
	ListAllCheckIDs(ctx context.Context) ([]uuid.UUID, error)
	EnqueueJobs(ctx context.Context, checkIDs []uuid.UUID) error
}

// WorkerStore is the subset of PostgresStore used by worker poll loops.
type WorkerStore interface {
	ClaimJob(ctx context.Context) (*ClaimedJob, error)
	CompleteJob(ctx context.Context, jobID, checkID uuid.UUID, result CheckResultInput) error
	ReapStaleJobs(ctx context.Context, olderThan time.Duration) (int64, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pool.Ping: %w", err)
	}
	return pool, nil
}

func (s *PostgresStore) Register(ctx context.Context, endpoint string) (*model.Check, error) {
	var c model.Check
	err := s.pool.QueryRow(ctx,
		`INSERT INTO checks (endpoint) VALUES ($1)
		 ON CONFLICT (endpoint) DO UPDATE SET endpoint = EXCLUDED.endpoint
		 RETURNING id, endpoint, created_at`,
		endpoint,
	).Scan(&c.ID, &c.Endpoint, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	return &c, nil
}

func (s *PostgresStore) GetCheck(ctx context.Context, id uuid.UUID) (*model.Check, error) {
	var c model.Check
	err := s.pool.QueryRow(ctx,
		`SELECT id, endpoint, created_at FROM checks WHERE id = $1`,
		id,
	).Scan(&c.ID, &c.Endpoint, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get check: %w", err)
	}
	return &c, nil
}

func (s *PostgresStore) GetLatestResult(ctx context.Context, checkID uuid.UUID) (*model.CheckResult, error) {
	var r model.CheckResult
	err := s.pool.QueryRow(ctx,
		`SELECT id, check_id, job_id, status_code, status_text, duration_ms, error, checked_at
		 FROM check_results
		 WHERE check_id = $1
		 ORDER BY checked_at DESC
		 LIMIT 1`,
		checkID,
	).Scan(&r.ID, &r.CheckID, &r.JobID, &r.StatusCode, &r.StatusText, &r.DurationMs, &r.Error, &r.CheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest result: %w", err)
	}
	return &r, nil
}

func (s *PostgresStore) DeleteCheck(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM checks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete check: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListChecks(ctx context.Context, limit, offset int) ([]*model.Check, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, endpoint, created_at FROM checks ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list checks: %w", err)
	}
	defer rows.Close()

	checks := make([]*model.Check, 0)
	for rows.Next() {
		var c model.Check
		if err := rows.Scan(&c.ID, &c.Endpoint, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan check: %w", err)
		}
		checks = append(checks, &c)
	}
	return checks, rows.Err()
}

func (s *PostgresStore) EnqueueJob(ctx context.Context, checkID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO check_jobs (check_id) VALUES ($1)`, checkID)
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListAllCheckIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM checks`)
	if err != nil {
		return nil, fmt.Errorf("list check ids: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) EnqueueJobs(ctx context.Context, checkIDs []uuid.UUID) error {
	if len(checkIDs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, id := range checkIDs {
		batch.Queue(`INSERT INTO check_jobs (check_id) VALUES ($1)`, id)
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("enqueue jobs: %w", err)
	}
	return nil
}

func (s *PostgresStore) ClaimJob(ctx context.Context) (*ClaimedJob, error) {
	var j ClaimedJob
	err := s.pool.QueryRow(ctx, `
		WITH claimed AS (
			UPDATE check_jobs
			SET status = 'running', claimed_at = now()
			WHERE id = (
				SELECT id FROM check_jobs
				WHERE status = 'pending'
				ORDER BY scheduled_at
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, check_id
		)
		SELECT claimed.id, claimed.check_id, checks.endpoint
		FROM claimed
		JOIN checks ON checks.id = claimed.check_id
	`).Scan(&j.JobID, &j.CheckID, &j.Endpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	return &j, nil
}

func (s *PostgresStore) CompleteJob(ctx context.Context, jobID, checkID uuid.UUID, result CheckResultInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`UPDATE check_jobs SET status = 'done', done_at = now() WHERE id = $1`,
		jobID,
	); err != nil {
		return fmt.Errorf("update job: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO check_results (check_id, job_id, status_code, status_text, duration_ms, error)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		checkID, jobID, result.StatusCode, result.StatusText, result.DurationMs, result.Error,
	); err != nil {
		return fmt.Errorf("insert result: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *PostgresStore) ReapStaleJobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	tag, err := s.pool.Exec(ctx,
		`UPDATE check_jobs SET status = 'pending', claimed_at = NULL
		 WHERE status = 'running' AND claimed_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("reap stale jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}
