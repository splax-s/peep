# peep Copilot Instructions

These rules are permanent. Apply them across all files, commits, and conversations in this repository.


peep Copilot Instructions (Kubernetes Edition)

These rules are permanent. Apply them across all files, commits, and conversations in this repository.

1. Core Rules

Never switch languages away from Go for backend services or Nginx for proxy/routing layers.

Always follow the peep architecture:

api/
builder/
dashboard/
nginx/
db/
ci/
shared Docker tooling
supporting infrastructure


Dashboard must remain Next.js.

Focus every change on backend quality, security, and performance.

Avoid frameworks unless absolutely necessary; prefer Go standard library and minimal dependencies.

Structure Go code as modular packages with explicit error handling and clear boundaries.

Produce production-grade solutions:

Safe error management

Structured JSON logging

Database migrations

Clean architecture layering (pkg/, service, repository)

2. Kubernetes/Minikube Deployment Rules

All services run as Kubernetes Deployments, one pod per logical service.

Expose services internally via ClusterIP, and externally via Nginx Ingress.

Use Kubernetes Secrets for all sensitive environment variables (database credentials, API keys, JWT secrets).

Minikube is the local development cluster; deployments should mirror future production clusters.

Avoid binding local Docker ports; rely on Ingress routing to expose endpoints.

Use PersistentVolumeClaims for stateful services like Postgres.

CI/CD jobs or build tasks may run as Kubernetes Jobs or CronJobs.

3. Service Mapping
Component	Kubernetes Object	Notes
api/	Deployment + ClusterIP Service	Route via Ingress /api
builder/	Deployment + ClusterIP Service	Route via Ingress /builder
dashboard/	Deployment + ClusterIP Service	Route via Ingress /dashboard
nginx/	Deployment + ClusterIP Service	Can use Nginx Ingress Controller; acts as reverse proxy
db/	StatefulSet + Service + PVC	Postgres storage persists via volume claims
ci/	Job / CronJob	For builds, automated tasks, migrations
Shared Docker tooling	Dockerfile + build in Minikube	Only used to create images referenced in Deployments
4. Phased Work
Phase 1 – Runtime Telemetry Pipeline

Build ingestion API in Go.

PostgreSQL schema: runtime_events table + rollups.

SSE/WebSocket fan-out for real-time metrics.

Lightweight SDK/middleware for apps & Nginx to emit structured request metrics (latency percentiles, throughput, errors).

Separate build logs from runtime logs.

Phase 2 – Dashboard Updates

Update dashboard IA with dedicated tabs for build logs vs runtime insights.

Include filtering/search, trend charts, and Framer Motion UX transitions.

Ensure dashboard consumes metrics from Kubernetes-deployed backend services.

Phase 3 – Environment Hierarchy

Implement dev/staging/prod environments.

Use Kubernetes Secrets per environment to manage env-vars securely.

Support audit trails and rollback UX.

Extend backend schemas/APIs for environment versioning.

Phase 4 – peep CLI

Go-based CLI with commands:

login/device-code flow

deploy

logs follow

env management

Distribute via Homebrew tap (brew install peep) and npm global shim (npm install -g peep).

Use GoReleaser automation.

Phase 5 – Marketing Landing Page

Next.js page with placeholder copy, FAQ, Framer Motion animations, CTAs.

Keep proprietary assets private.

5. Deployment & Runtime Rules

Docker images are built inside Minikube (eval $(minikube docker-env)).

Apply Kubernetes manifests for secrets, stateful services, deployments, and Ingress.

Add /etc/hosts entry for local domains (e.g., 127.0.0.1 peep.com).

All metrics and logs must be emitted as structured JSON.

Always maintain separation between build logs and runtime logs.

Minikube pods must be resource-limited to avoid CPU/RAM hogging.

6. Security & Quality

Secrets are never hardcoded; always reference Kubernetes Secrets.

Explicit error handling and defensive programming in Go.

No unnecessary third-party libraries; minimal dependencies only.

All backend APIs must return structured JSON with consistent error codes.

Follow clean architecture: separate pkg, service, and repository layers.


