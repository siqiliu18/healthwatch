package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siqiliu18/healthwatch/internal/model"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	Register(ctx context.Context, endpoint string) (*model.Check, error)
	GetCheck(ctx context.Context, id uuid.UUID) (*model.Check, error)
	GetLatestResult(ctx context.Context, checkID uuid.UUID) (*model.CheckResult, error)
	DeleteCheck(ctx context.Context, id uuid.UUID) error
	ListChecks(ctx context.Context, limit, offset int) ([]*model.Check, error)
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
