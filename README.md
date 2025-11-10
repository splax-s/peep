# peep

peep is a Kubernetes-first control plane that joins runtime observability, deployment automation, and environment policy in one platform. The stack is Go services on the backend, a Next.js dashboard for operators, and an edge Nginx layer that fronts every request.

## Highlights

- Unified runtime telemetry pipeline with structured JSON logs, rollups, and live streams.
- Clean architecture Go services with clear pkg/service/repository boundaries.
- Next.js dashboard delivering build insights, runtime analytics, and environment management.
- Minikube-ready Kubernetes manifests covering Deployments, StatefulSets, Secrets, and Ingress.
- Go-based CLI packaged via GoReleaser with Homebrew tap and npm distribution shim.

## Architecture

```
client -> ingress (nginx) -> dashboard (Next.js)
                         -> api (Go) -> postgres (StatefulSet)
                         -> builder (Go) -> runtime jobs (K8s)
```

| Layer      | Description |
|------------|-------------|
| `api/`     | Go HTTP API that powers authentication, telemetry ingestion, and orchestration.
| `builder/` | Background workers and build pipeline automation written in Go.
| `dashboard/` | Next.js 16 app with runtime/build tabs, powered by Framer Motion interactions.
| `nginx/`   | Edge router and Ingress controller configuration.
| `db/`      | PostgreSQL manifests with PersistentVolumeClaims for stateful data.
| `ci/`      | Jobs and scripts for migrations, release automation, and cluster tasks.
| `cmd/`     | Go CLI, npm shim, and release docs handled by GoReleaser workflows.

## Telemetry and Environments

- Runtime events land in PostgreSQL with percentile rollups and real-time SSE fan-out.
- Environment hierarchy (dev/staging/prod) includes audit trails, version history, and rollback metadata.
- Structured JSON logging is mandatory across services to keep ingestion predictable.
- Metrics and logs intentionally separate build versus runtime sources for clarity.

## Getting Started

1. Install prerequisites: Docker, Minikube, kubectl, mkcert (or openssl).
2. Clone the repository and enter the root directory.
3. Follow `docs/minikube.md` to boot Minikube, build images inside the cluster, and apply manifests.
4. Add `127.0.0.1 peep.com` to `/etc/hosts` and start a port-forward (`kubectl port-forward -n peep svc/edge-nginx 8080:80`).
5. Visit `http://peep.com:8080` (or via tunnel) to reach the marketing landing page and dashboard login.

## CLI Distribution

- Build artifacts are managed with `.goreleaser.yaml` and the `release-cli` GitHub Actions workflow.
- Homebrew tap: `splax-s/homebrew-peep` receives versioned tarballs for `brew install peep`.
- npm shim: `cmd/peep/npm` installs a pinned binary for macOS, Linux, and Windows users.
- Device-code authentication flow enables browserless login from the CLI.

## Directory Overview

```
api/           Go services for API surface and telemetry ingestion
builder/       Go build pipeline workers
cmd/           Go CLI, npm shim, release docs
dashboard/     Next.js dashboard and marketing landing page
k8s/           Kubernetes base manifests (Deployments, Ingress, Secrets)
nginx/         Edge Nginx configuration
ci/            Automation jobs and scripts
pkg/           Shared Go packages and domain logic
docs/          Operational guides (Minikube, releasing, etc.)
```

## Development Notes

- Always build Docker images inside Minikube (`eval "$(minikube docker-env)"`).
- Secrets live in Kubernetes; never commit credentials to the repository.
- Go code uses explicit error handling, structured logging, and minimal dependencies.
- Dashboard animations rely on Framer Motion but default to reduced-motion friendly fallbacks.

## Support and Future Work

- Phase 1 telemetry pipeline and Phase 4 CLI are live; Phase 5 marketing site is in progress.
- Upcoming work includes staging/prod overlays, audit-ready rollout tooling, and enhanced analytics.
- For bugs or feature requests, open an issue or follow TODO items in `TODO.md`.
