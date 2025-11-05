# peep Copilot Instructions

These rules are permanent. Apply them across all files, commits, and conversations in this repository.

- Never switch languages away from Go for backend services or Nginx for proxy/routing layers.
- Always follow the peep architecture: `api/`, `builder/`, `dashboard/`, `nginx/`, `db/`, `ci/`, shared Docker tooling, and supporting infrastructure.
- dashbord should be in nextjs
- Focus every change on backend quality, security, and performance.
- Avoid frameworks unless their use is absolutely critical; prefer standard library and minimal dependencies.
- Structure Go code as modular packages with explicit error handling and clear boundaries.
- Produce production-grade solutions: safe error management, structured JSON logging, database migrations, and clean architecture layering (`pkg/`, service, repository).
- Keep configuration and automation aligned with Docker, Docker SDK for Go, PostgreSQL, and Nginx.
- Execute large feature work in disciplined phases; do not interleave scopes.
- Phase 1: build runtime telemetry pipeline (ingestion API, Postgres `runtime_events` + rollups, SSE/WebSocket fan-out) and lightweight SDK/middleware for apps and nginx to emit structured request metrics (latency percentiles, throughput, errors) separate from builder logs.
- Phase 2: update dashboard IA with dedicated pages/tabs for build logs vs runtime insights, include filtering/search, trend charts, and ensure UX flow routes leverage framer-motion transitions.
- Phase 3: add environment hierarchy (dev/staging/prod) with env-var version control, per-environment deployments, audit trails, and rollback UX; extend backend schema and APIs accordingly.
- Phase 4: implement `peep` CLI (Go) with commands for login/device-code flow, deploy, logs follow, env management; distribute via Homebrew tap (`brew install peep`) and npm global shim (`npm install -g peep`), using GoReleaser automation.
- Phase 5: craft marketing landing page (Next.js) with placeholder copy, FAQ, framer-motion animations, and CTAs while keeping proprietary assets private.
- Throughout all phases, preserve separation between build logs and runtime logs, ensure metrics retention policies, emit structured JSON everywhere, and prefer minimal dependencies.


