# HealthWatch

![CI](https://github.com/siqiliu18/healthwatch/actions/workflows/ci.yml/badge.svg)

Distributed URL health checker demonstrating Kubernetes autoscaling via KEDA.
Worker pods scale based on job queue depth — not CPU.

> See [docs/design.md](docs/design.md) for architecture, schema, and development phases.

## Demo

Workers scale from 2 → 20 replicas as the job queue floods, then drain back down.

<video src="https://github.com/user-attachments/assets/2a1d4bd9-5cb2-4edb-84dd-649349eae308" controls width="100%"></video>

## Stack

Go · Kubernetes · KEDA · PostgreSQL · Redis · GKE

## GKE Deployment

### One-time setup

```bash
# Authenticate and set project
gcloud auth login
gcloud config set project healthwatch-siqi-2026

# Enable APIs
gcloud services enable container.googleapis.com artifactregistry.googleapis.com

# Create cluster and registry
gcloud container clusters create healthwatch \
  --zone us-central1-a \
  --num-nodes 3 \
  --machine-type e2-standard-2 \
  --disk-size 20

gcloud artifacts repositories create healthwatch \
  --repository-format docker \
  --location us-central1

# Install KEDA
helm repo add kedacore https://kedacore.github.io/charts && helm repo update
helm install keda kedacore/keda --namespace keda --create-namespace
```

### Build and push images (Apple Silicon → amd64 for GKE)

```bash
gcloud auth configure-docker us-central1-docker.pkg.dev

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/service ./cmd/api
docker build -t us-central1-docker.pkg.dev/healthwatch-siqi-2026/healthwatch/api:latest .
docker push us-central1-docker.pkg.dev/healthwatch-siqi-2026/healthwatch/api:latest

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/service ./cmd/worker
docker build -t us-central1-docker.pkg.dev/healthwatch-siqi-2026/healthwatch/worker:latest .
docker push us-central1-docker.pkg.dev/healthwatch-siqi-2026/healthwatch/worker:latest
```

### Deploy

```bash
gcloud container clusters get-credentials healthwatch --zone us-central1-a

kubectl apply -f k8s/secret.yaml        # copy from k8s/secret.example.yaml first
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/postgres-statefulset.yaml
kubectl apply -f k8s/redis-statefulset.yaml
kubectl apply -f k8s/keda-scaledobject.yaml
kubectl apply -f k8s/gke/
kubectl get pods -w
```

### Run the autoscaling demo

```bash
# Terminal 1 — watch workers scale
watch -n2 kubectl get pods

# Terminal 2 — flood the queue (1s-delay endpoints give KEDA time to react)
go run ./cmd/loadgen \
  -n 500 \
  -api http://<EXTERNAL-IP> \
  -url 'https://httpbin.org/delay/1?id=%d'
```

Get the external IP with: `kubectl get svc healthwatch-api`

### Reset between demo runs

```bash
kubectl scale deployment/healthwatch-api --replicas=0
kubectl scale deployment/healthwatch-worker --replicas=2
# wait ~10s, then:
kubectl scale deployment/healthwatch-api --replicas=1
# queue auto-fills from registered URLs on next scheduler tick (~10s)
```

### Teardown (stop GKE billing)

```bash
gcloud container clusters delete healthwatch --zone us-central1-a
gcloud artifacts repositories delete healthwatch --location us-central1
```

## Local Development (Rancher Desktop — dockerd mode)

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

### Prerequisites (one-time cluster setup)

**KEDA** must be installed before applying `k8s/keda-scaledobject.yaml`:

```bash
helm repo add kedacore https://kedacore.github.io/charts
helm repo update
helm install keda kedacore/keda --namespace keda --create-namespace
kubectl get pods -n keda -w
```

**Redis image** must be pulled before deploying:

```bash
docker pull redis:7-alpine
```

### Deploy / Teardown

```bash
# First time: create secret (gitignored)
cp k8s/secret.example.yaml k8s/secret.yaml
kubectl apply -f k8s/secret.yaml

# Switch to Rancher Desktop context if needed
kubectl config use-context rancher-desktop

kubectl apply -f k8s/
kubectl get pods -w

# Teardown (secret survives — reapply it next time)
kubectl delete -f k8s/
```

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/checks` | Register a URL |
| `GET` | `/checks` | List all registered URLs |
| `GET` | `/checks/:id` | Get latest result for a URL |
| `DELETE` | `/checks/:id` | Unregister a URL |
| `POST` | `/checks/:id/try` | Trigger an immediate check |
| `GET` | `/metrics/queue-depth` | Pending job count (polled by KEDA) |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/healthz` | Liveness probe |

