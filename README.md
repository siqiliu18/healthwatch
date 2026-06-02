# HealthWatch

Distributed URL health checker demonstrating Kubernetes autoscaling via KEDA.
Worker pods scale based on job queue depth — not CPU.

> See [docs/design.md](docs/design.md) for architecture, schema, and development phases.

## Stack

Go · Kubernetes · KEDA · PostgreSQL · Redis · GKE

## Building Images (Rancher Desktop — dockerd mode)

Rancher Desktop runs k3s with Docker as the container runtime, so images built with
`docker build` are directly visible to k8s — no `nerdctl load` needed.

Because Docker Hub pulls time out on this setup, the Dockerfile skips the Go builder
stage. Build the Linux binary locally first, then build the image:

```bash
# API
CGO_ENABLED=0 GOOS=linux go build -o bin/service ./cmd/api
docker build -t healthwatch-api:latest .

# Worker
CGO_ENABLED=0 GOOS=linux go build -o bin/service ./cmd/worker
docker build -t healthwatch-worker:latest .
```

> Apple Silicon: add `GOARCH=arm64`. Intel: add `GOARCH=amd64`.

## Prerequisites (one-time cluster setup)

**KEDA** must be installed before applying `k8s/keda-scaledobject.yaml`.
KEDA pulls from `ghcr.io` (not Docker Hub), so it works even when Docker Hub times out:

```bash
helm repo add kedacore https://kedacore.github.io/charts
helm repo update
helm install keda kedacore/keda --namespace keda --create-namespace
kubectl get pods -n keda -w   # wait until keda pods are Running
```

**Redis image** must be pulled before deploying (Docker Hub connectivity required once):

```bash
docker pull redis:7-alpine
```

## Deploy / Teardown

```bash
# First time: create secret (gitignored)
cp k8s/secret.example.yaml k8s/secret.yaml
# edit k8s/secret.yaml with real DATABASE_URL
kubectl apply -f k8s/secret.yaml

# Switch to Rancher Desktop context if needed
kubectl config use-context rancher-desktop

# Deploy everything
kubectl apply -f k8s/
kubectl get pods -w

# Teardown (secret survives — reapply it next time)
kubectl delete -f k8s/
```

## Testing Phase 1 — Core API + PostgreSQL

```bash
kubectl port-forward svc/healthwatch-api 8080:80

# Register a URL
curl -X POST localhost:8080/checks \
  -H 'Content-Type: application/json' \
  -d '{"endpoint":"https://example.com"}'

# List all registered URLs
curl localhost:8080/checks
```

Phase 1 is done when the POST returns a JSON object with an `id`.

## Testing Phase 2 — Worker + SKIP LOCKED queue

```bash
kubectl port-forward svc/healthwatch-api 8080:80

# Register a URL
curl -X POST localhost:8080/checks \
  -H 'Content-Type: application/json' \
  -d '{"endpoint":"https://example.com"}'
# note the returned id

# Wait ~5 seconds for the worker to pick up and complete the job, then:
curl localhost:8080/checks/<id>
```

Phase 2 is done when `latest_result` is populated with a real `status_code` and
`duration_ms` — confirming the worker claimed the job, pinged the URL, and wrote the
result.

## Testing Phase 3 — Redis cache + KEDA autoscaling

```bash
kubectl port-forward svc/healthwatch-api 8080:80

# Verify the metrics endpoint KEDA polls
curl localhost:8080/metrics/queue-depth
# → {"pending":0}  (0 is correct when the queue is drained)

# Register a URL and fetch its result — second call is served from Redis cache
curl -X POST localhost:8080/checks \
  -H 'Content-Type: application/json' \
  -d '{"endpoint":"https://example.com"}'
curl localhost:8080/checks/<id>
```

Phase 3 is done when:
- `GET /metrics/queue-depth` returns `{"pending": N}`
- All four pods are Running: api, postgres, redis, worker (×2)
- KEDA ScaledObject is created without error (`kubectl get scaledobject`)

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/checks` | Register a URL |
| `GET` | `/checks` | List all registered URLs |
| `GET` | `/checks/:id` | Get latest result for a URL |
| `DELETE` | `/checks/:id` | Unregister a URL |
| `POST` | `/checks/:id/try` | Trigger an immediate check |
| `GET` | `/metrics/queue-depth` | Pending job count (polled by KEDA) |
| `GET` | `/healthz` | Liveness probe |

## Heritage

Evolved from the [blizzard](../blizzard/) take-home — a single-binary health checker whose architectural limits (shared file state, goroutine-per-URL) made it unscalable across multiple instances. HealthWatch closes every one of those gaps.
