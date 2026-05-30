# HealthWatch

Distributed URL health checker demonstrating Kubernetes autoscaling via KEDA.
Worker pods scale based on job queue depth — not CPU.

> See [docs/design.md](docs/design.md) for architecture, schema, and development phases.

## Stack

Go · Kubernetes · KEDA · PostgreSQL · Redis · GKE

## Quick Start (Phase 1 — Rancher Desktop)

**Prerequisites:** Rancher Desktop running (dockerd or containerd runtime).

```bash
# 1. Build the image and load it into k3s's containerd namespace
#    (nerdctl build requires buildkitd which can be flaky — this pipe is more reliable)
docker build -t healthwatch-api:latest .
docker save healthwatch-api:latest | nerdctl --namespace k8s.io load

# 2. Create your secret (gitignored)
cp k8s/secret.example.yaml k8s/secret.yaml
kubectl apply -f k8s/secret.yaml

# 3. Deploy Postgres + API
kubectl apply -f k8s/

# 4. Wait for pods to be ready
kubectl get pods -w

# 5. Test the API
kubectl port-forward svc/healthwatch-api 8080:80

curl -X POST localhost:8080/checks \
  -H 'Content-Type: application/json' \
  -d '{"endpoint":"https://example.com"}'

curl localhost:8080/checks
```

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/checks` | Register a URL |
| `GET` | `/checks` | List all registered URLs |
| `GET` | `/checks/:id` | Get latest result for a URL |
| `DELETE` | `/checks/:id` | Unregister a URL |
| `GET` | `/healthz` | Liveness probe |

## Heritage

Evolved from the [blizzard](../blizzard/) take-home — a single-binary health checker whose architectural limits (shared file state, goroutine-per-URL) made it unscalable across multiple instances. HealthWatch closes every one of those gaps.
