# Testing Strategy

## API service

- Unit tests live alongside their packages (e.g. `api/internal/service/...`).
- Run the full suite with:

  ```sh
  cd api
  go test ./...
  ```

- For targeted runs, specify the package path:

  ```sh
  cd api
  go test ./internal/service/deploy -run TestProcessCallback
  ```

## Builder service

- Unit tests mirror the API layout under `builder/internal/...`.
- Execute all builder tests:

  ```sh
  cd builder
  go test ./...
  ```

- Example focused run:

  ```sh
  cd builder
  go test ./internal/service/deploy -run TestSuppression
  ```

## Integration flow

1. Start the compose stack:

    ```sh
    docker compose up -d --build
    ```

2. Create a user, team, project, and trigger a deployment using the HTTP endpoints.

3. Inspect builder and API logs to ensure the deployment completes and ingress updates fire.

### Helpful commands

- Builder logs:

  ```sh
  docker compose logs -f builder
  ```

- API logs:

  ```sh
  docker compose logs -f api
  ```

- Verify ingress configuration:

  ```sh
  docker compose exec nginx ls /etc/nginx/conf.d
  ```

## Design choices

- Keep unit tests fast and deterministic with lightweight fakes for repositories and external services.
- Run `go fmt` and `go test ./...` in both `api/` and `builder/` before committing.
- Integration testing is performed manually today; future work includes dockerized smoke tests that exercise the end-to-end flow automatically.
