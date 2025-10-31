# peep Copilot Instructions

These rules are permanent. Apply them across all files, commits, and conversations in this repository.

- Never switch languages away from Go for backend services or Nginx for proxy/routing layers.
- Always follow the peep architecture: `api/`, `builder/`, `dashboard/`, `nginx/`, `db/`, `ci/`, shared Docker tooling, and supporting infrastructure.
- Focus every change on backend quality, security, and performance.
- Avoid frameworks unless their use is absolutely critical; prefer standard library and minimal dependencies.
- Structure Go code as modular packages with explicit error handling and clear boundaries.
- Produce production-grade solutions: safe error management, structured JSON logging, database migrations, and clean architecture layering (`pkg/`, service, repository).
- Keep configuration and automation aligned with Docker, Docker SDK for Go, PostgreSQL, and Nginx.
