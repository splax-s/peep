# Ingress Management

Peep uses an internal ingress service to generate proxy configuration for active deployments. The API watches builder callbacks, stores container metadata, and writes one config file per project into `nginx/conf.d`.

## Directory layout

```text
nginx/
├─ nginx.conf       # base configuration, includes conf.d/*.conf
└─ conf.d/          # generated per-project configs
```

Configs are created or removed automatically when deployment status updates stream in. Each file maps the project slug (e.g. `my-app.local.peep`) to the running container host/port recorded by the builder.

The repository includes a static `nginx/conf.d/000-default.conf` that acts as a catch-all host, returning the branded error pages when no project matches. Keep this file in place to prevent stray hosts from leaking another project’s deployment.

If a request targets an inactive route the ingress returns a branded 404 page served from `nginx/errors/404.html`. Transient upstream failures surface a corresponding status page from `nginx/errors/502.html`. Update those templates to align the messaging and styling with your brand.

## Runtime configuration

Environment variables for the API service control ingress behaviour:

| Variable | Description | Default |
| --- | --- | --- |
| `NGINX_CONFIG_PATH` | Directory where configs are written. Should be shared with the proxy process. | `/workspace/nginx/conf.d` in docker-compose |
| `INGRESS_DOMAIN_SUFFIX` | Host suffix appended to project slugs. | `.local.peep` |
| `NGINX_RELOAD_COMMAND` | Optional shell command executed after config changes to reload the proxy. Leave empty if Docker-based reloads are configured. | _empty_ |
| `NGINX_CONTAINER_NAME` | Name of the proxy container to signal with `SIGHUP` via the Docker socket when no reload command is supplied. | `peep-nginx` |

If `NGINX_RELOAD_COMMAND` is unset the service logs a debug message and skips reloads. In local development run `docker compose exec nginx nginx -s reload` whenever configs change.

## Docker Compose workflow

The `docker-compose.yml` definition includes a lightweight reverse proxy service that shares the repository’s `nginx/` directory with the API container. The API container mounts the Docker socket and, by default, sends `SIGHUP` to `peep-nginx` after configuration changes. After a deployment reaches the `running` state, you can inspect the generated config or trigger a reload manually if needed:

```sh
docker compose exec nginx ls /etc/nginx/conf.d

docker compose exec nginx nginx -s reload
```

Access the deployment via `http://<project-slug>.local.peep:8080` (the compose file maps container port 80 to host 8080). Automatic reloads are handled through the Docker socket; if you prefer a shell-based command, set `NGINX_RELOAD_COMMAND` and omit `NGINX_CONTAINER_NAME`.

> **Note:** The generated configs default to proxying via `host.docker.internal`. On Linux hosts where this alias is unavailable, update the ingress service template or provide a fixed host IP before reloading the proxy.

## Production considerations

- Ensure the API has permission to write to the proxy config directory and trigger reloads (e.g. via a dedicated script or RPC).
- Update `INGRESS_DOMAIN_SUFFIX` to match your public DNS pattern.
- Secure the ingress by layering TLS (`listen 443 ssl`) and rate-limiting in the generated templates. Extend `Service.writeConfig` if you need advanced routing.
