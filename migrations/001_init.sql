CREATE TABLE checks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint    TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE check_jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    check_id     UUID NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending',
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at   TIMESTAMPTZ,
    done_at      TIMESTAMPTZ
);
CREATE INDEX idx_check_jobs_pending ON check_jobs (status, scheduled_at)
    WHERE status = 'pending';

CREATE TABLE check_results (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    check_id    UUID NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
    job_id      UUID REFERENCES check_jobs(id),
    status_code INT,
    status_text TEXT,
    duration_ms INT,
    error       TEXT,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_check_results_check_id ON check_results (check_id, checked_at DESC);
