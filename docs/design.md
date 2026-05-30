# HealthWatch — Design Document

## Background

HealthWatch is the distributed rebuild of [blizzard](../blizzard/), a Go HTTP server written as a Blizzard take-home assignment. Blizzard works well at small scale but has three hard architectural limits that make it unfit for production:

| Blizzard (monolith) | Why it breaks at scale |
|---|---|
| `store.go` — in-memory map + `data.json` | Two instances diverge immediately; file write on every tick is a bottleneck |
| `checker.go` — goroutines per URL, buffered channel semaphore | Semaphore caps concurrency per process, not across machines; can't distribute work |
| `main.go` — single binary | No independent scaling; API traffic and checker load are coupled |

The specific failure that surfaced during the take-home: registering 999 URLs with unbounded goroutines crashed the home router's DNS resolver. The semaphore fix worked for one process. Across multiple pods it still breaks — every pod pings every URL every tick.

---

## First Principle

**A health checker at scale is a job queue problem.**

At 10 URLs, a for-loop works. At 10,000 URLs, you must decouple scheduling from execution:

1. Enqueue one check job per registered URL per tick
2. Stateless worker pods consume jobs from the queue
3. Worker count tracks queue depth — not CPU

Every design decision below follows from this.

---

## Architecture

```
Client (curl / load-gen CLI)
          │
          ▼
  ┌───────────────┐        ┌─────────────────────┐
  │  API Service  │◄──────►│  Redis              │
  │  (stateless)  │        │  latest result cache│
  └───────┬───────┘        └─────────────────────┘
          │ INSERT check_jobs                ▲
          ▼                                 │ write result
  ┌───────────────────────────────────────────────────┐
  │                   PostgreSQL                       │
  │  checks table  │  check_jobs table  │  check_results table │
  └───────────────────────────────────────────────────┘
          │ SKIP LOCKED                     │
          ▼                                 │
  ┌───────────────┐                         │
  │ Worker Pods   │─────────────────────────┘
  │ (KEDA-scaled) │
  └───────────────┘
          ▲
          │ ScaledObject watches pending job count
  ┌───────────────┐
  │     KEDA      │
  └───────────────┘
```

### Why not Kafka?

Postgres `SKIP LOCKED` is sufficient at this scale. Each worker atomically claims one job, pins it, and releases it on completion. No separate broker to deploy, no partition assignment to manage. If the project outgrows Postgres throughput, the interface is narrow enough to swap in Kafka without touching the worker logic.

---

## Component Design

### API Service (`cmd/api/`)

Stateless Go HTTP server. Responsibilities:
- Register/delete URLs → write to `checks` table
- Schedule a check immediately (`POST /checks/:id/try`) → insert into `check_jobs`
- Return latest results → read from Redis, fall back to `check_results`

No direct coupling to workers. Never touches `check_jobs` for reads.

### Worker Service (`cmd/worker/`)

Stateless Go process. Responsibilities:
- Poll `check_jobs` for pending rows using `SKIP LOCKED`
- Ping the target URL (evolved from `blizzard/checker.go:ping()`)
- Write result to `check_results` with `ON CONFLICT DO UPDATE` (idempotent)
- Update Redis cache with latest result
- Release/complete the job row

Workers are interchangeable. KEDA scales the `worker` Deployment based on the count of `pending` rows in `check_jobs`.

### Scheduler (`internal/worker/scheduler.go`)

Runs inside the API service as a background goroutine. Every `checkFrequency` seconds it bulk-inserts one `check_job` row per registered URL. This replaces `blizzard/checker.go:Start()` but does not do any pinging — it only enqueues.

### Load Generator (`cmd/loadgen/`)

CLI tool for demo recording. Registers N URLs via the API in parallel, then polls queue depth until it drains. Drives the autoscaling demo.

---

## Database Schema

```sql
-- Registered URLs
CREATE TABLE checks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint    TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Work queue: one row per pending check
-- SKIP LOCKED is the distribution mechanism
CREATE TABLE check_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    check_id    UUID NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending',   -- pending | running | done | failed
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at  TIMESTAMPTZ,
    done_at     TIMESTAMPTZ
);
CREATE INDEX idx_check_jobs_pending ON check_jobs (status, scheduled_at)
    WHERE status = 'pending';

-- Results: full history, one row per completed check
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
```

**Why split `checks` and `check_results`?**
Blizzard merged registration and latest result into one `HealthCheck` struct. That conflates two concerns: what you're watching vs what you observed. Splitting them allows full history and makes the work queue (`check_jobs`) a clean join table.

---

## Work Queue: SKIP LOCKED

Each worker runs this in a transaction:

```sql
BEGIN;

UPDATE check_jobs
SET status = 'running', claimed_at = now()
WHERE id = (
    SELECT id FROM check_jobs
    WHERE status = 'pending'
    ORDER BY scheduled_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, check_id;

-- (worker pings the URL)

UPDATE check_jobs SET status = 'done', done_at = now() WHERE id = $jobID;

INSERT INTO check_results (check_id, job_id, status_code, status_text, duration_ms, error, checked_at)
VALUES ($checkID, $jobID, $code, $text, $ms, $err, now())
ON CONFLICT (job_id) DO UPDATE
    SET status_code = EXCLUDED.status_code,
        status_text = EXCLUDED.status_text,
        duration_ms = EXCLUDED.duration_ms,
        error       = EXCLUDED.error,
        checked_at  = EXCLUDED.checked_at;

COMMIT;
```

`SKIP LOCKED` means a worker that finds a locked row moves on immediately rather than blocking. No worker ever waits on another. At-least-once delivery: if a worker crashes, its job stays `running` until a reaper goroutine resets stale jobs (any `running` row older than 2× ping timeout → back to `pending`).

---

## Redis Cache

Key schema:

```
check:result:{check_id}   →  JSON blob of latest CheckResult   TTL: 5m
check:queue:depth         →  INT (count of pending jobs)       TTL: 35s
```

- Workers write `check:result:{id}` after every successful result
- API reads `check:result:{id}` first; falls back to `check_results` table on miss
- `check:queue:depth` is written by the KEDA metrics endpoint (see below)

---

## API Reference

```
POST   /checks                  Register a URL
GET    /checks                  List all registered URLs (paginated)
GET    /checks/:id              Get latest result for a URL
DELETE /checks/:id              Unregister a URL
POST   /checks/:id/try          Trigger an immediate check (inserts a priority job)

GET    /metrics/queue-depth     Returns {"pending": N} — polled by KEDA
GET    /healthz                 Liveness probe
```

---

## KEDA ScaledObject

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: worker-scaledobject
spec:
  scaleTargetRef:
    name: healthwatch-worker
  minReplicaCount: 2
  maxReplicaCount: 20
  cooldownPeriod: 60
  triggers:
    - type: metrics-api
      metadata:
        targetValue: "50"           # scale up when pending/replicas > 50
        url: "http://healthwatch-api/metrics/queue-depth"
        valueLocation: "pending"
```

**Why `targetValue: 50`?** Each worker processes ~50 URLs/minute at default ping timeout. One replica per 50 pending jobs keeps latency within one check cycle.

**Why not CPU HPA?** A worker processing 10 lightweight URLs looks identical to one processing 0 — CPU stays near zero either way. Queue depth is the only accurate signal.

---

## Kubernetes Manifests (`k8s/`)

| File | Resource |
|---|---|
| `api-deployment.yaml` | API `Deployment` + `Service` (ClusterIP) |
| `worker-deployment.yaml` | Worker `Deployment` (no Service needed) |
| `keda-scaledobject.yaml` | KEDA `ScaledObject` for worker |
| `postgres-statefulset.yaml` | PostgreSQL `StatefulSet` + `PersistentVolumeClaim` |
| `redis-statefulset.yaml` | Redis `StatefulSet` + `PersistentVolumeClaim` |
| `configmap.yaml` | Non-secret config (check frequency, timeouts) |
| `secret.yaml` | DSN, Redis address (gitignored) |
| `ingress.yaml` | External access to API (optional) |

---

## File Structure

```
healthwatch/
├── cmd/
│   ├── api/
│   │   └── main.go              # API server entrypoint
│   ├── worker/
│   │   └── main.go              # Worker entrypoint
│   └── loadgen/
│       └── main.go              # Demo load generator
├── internal/
│   ├── api/
│   │   ├── handler.go           # HTTP handlers (register, get, delete, try, metrics)
│   │   └── server.go            # Server + router setup
│   ├── worker/
│   │   ├── worker.go            # Main poll loop (SKIP LOCKED claim → ping → write)
│   │   ├── checker.go           # URL ping logic (evolved from blizzard/checker.go)
│   │   └── scheduler.go         # Background goroutine: enqueue jobs on a ticker
│   ├── store/
│   │   ├── postgres.go          # All PostgreSQL queries
│   │   └── redis.go             # Redis cache read/write
│   └── model/
│       └── model.go             # Shared types: Check, CheckJob, CheckResult
├── k8s/
│   ├── api-deployment.yaml
│   ├── worker-deployment.yaml
│   ├── keda-scaledobject.yaml
│   ├── postgres-statefulset.yaml
│   ├── redis-statefulset.yaml
│   ├── configmap.yaml
│   └── ingress.yaml
├── docs/
│   └── design.md                # This file
├── go.mod
└── README.md
```

---

## Development Phases

Everything runs on Rancher Desktop (k3s) from Phase 1. No Docker Compose.

### Phase 1 — Core API + PostgreSQL on Rancher Desktop
- `internal/model`, `internal/store/postgres.go`, `internal/api/`
- `cmd/api/main.go`
- K8s manifests: API Deployment + PostgreSQL StatefulSet + ConfigMap
- Endpoints: POST /checks, GET /checks/:id, DELETE /checks
- **Done when:** `curl $(kubectl get svc healthwatch-api ...)` returns a registered URL

### Phase 2 — Worker + SKIP LOCKED queue
- `internal/worker/worker.go`, `internal/worker/checker.go` (port ping logic from blizzard)
- `internal/worker/scheduler.go` (ticker that enqueues jobs)
- `cmd/worker/main.go`
- K8s manifests: Worker Deployment
- **Done when:** register a URL, wait one tick, GET /checks/:id returns a real result

### Phase 3 — Redis cache + KEDA autoscaling
- `internal/store/redis.go` wired into API (read) and worker (write)
- `GET /metrics/queue-depth` endpoint
- K8s manifests: Redis StatefulSet + KEDA ScaledObject
- **Done when:** `kubectl get pods -w` shows workers scaling during `cmd/loadgen` run

### Phase 4 — Observability + load generator
- Prometheus metrics on `/metrics`
- `cmd/loadgen/main.go` — bulk-registers N URLs, polls queue depth until drain
- **Done when:** full autoscaling demo is reproducible end-to-end on Rancher Desktop

### Phase 5 — GKE + demo video + teardown
- Deploy to GKE Standard (single zone)
- Record 90-second autoscaling video
- Write README with architecture diagram + embedded video
- Delete GKE node pool
- **Done when:** video is in README, GitHub Actions CI is green

---

## Blizzard → HealthWatch Reference

Specific code being evolved, not rewritten from scratch:

| blizzard | healthwatch | Change |
|---|---|---|
| `checker.go:ping()` | `internal/worker/checker.go:ping()` | No change in logic; context cancellation added |
| `checker.go:Start()` | `internal/worker/scheduler.go` | Removed ping logic; now only enqueues jobs |
| `store.go:update()+flush()` | `internal/store/postgres.go` | File replaced by Postgres; SKIP LOCKED replaces mutex |
| `models.go:HealthCheck` | `internal/model/model.go` | Split into `Check` + `CheckResult` + `CheckJob` |
| `handlers.go` | `internal/api/handler.go` | Same endpoints; store is now an interface |
| `main.go` | `cmd/api/main.go` + `cmd/worker/main.go` | Single binary split into two deployable services |
