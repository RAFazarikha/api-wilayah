# API Wilayah Indonesia

Read-only Go API (chi) for provinsi, kabupaten/kota, kecamatan, desa/kelurahan, kodepos, and pulau. Data is embedded and held in memory; there is no database. Plan and decisions: `docs/PLAN.md`. Endpoints: `api/openapi.yaml` (served at `/docs`). Boundaries (polygons) are not in v1.

## Build the data

The SQL sources are read-only inputs; the artifact `data/wilayah.json.gz` is committed.

    go run ./cmd/builddata -wilayah-dir ../wilayah/db -kodepos-dir ../wilayah_kodepos/db

Validation fails hard on bad codes, missing parents, orphan kodepos, or level counts other than 38/514/7285/83762 (override with `-allow-count-mismatch`). `-data-version` and `-dasar` override the labels, which default to the newest `last edit` date and the Kepmendagri line from the `wilayah.sql` header. See `meta.catatan` in `/v1/meta`.

## Run

    go run ./cmd/api            # http://localhost:8080/docs
    go test -race ./...
    go test -bench . -run XXX ./internal/...

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | Public listener |
| `TRUSTED_PROXY_CIDRS` | empty | Comma-separated CIDRs/IPs of reverse proxies. `X-Forwarded-For` is used only when the TCP peer is in this list; empty means never trusted, so behind a proxy all clients share one rate-limit bucket (a warning is logged at start). Use the proxy's own IP or a narrow subnet: every container inside a trusted CIDR can forge `X-Forwarded-For`. Behind a CDN the address read is the CDN edge IP unless the CDN ranges are trusted |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | `10` / `20` | Per-IP token bucket; RPS must be a finite number >= 0, `0` disables |
| `CORS_ORIGINS` | `*` | Comma-separated allowed origins |
| `REQUEST_TIMEOUT` | `10s` | Per-request deadline |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown wait |
| `METRICS_ADDR` | `127.0.0.1:9090` | Separate Prometheus listener (`/metrics`); empty disables |
| `LOG_LEVEL` | `info` | slog level (JSON on stdout) |

## Deploy on Coolify

Create a Docker Compose resource from this repo (`docker-compose.yml`, image built from `Dockerfile`, distroless, non-root). Assign the domain in Coolify; Traefik terminates TLS and routes to container port 8080, and no host port is published. Set `TRUSTED_PROXY_CIDRS` to the Traefik container IP or a narrow subnet (not the whole coolify network if avoidable, since other containers on it could then spoof `X-Forwarded-For`) so rate limiting sees real client IPs; if left empty every client shares one bucket. If a CDN sits in front of Traefik, the IP seen is the CDN edge, not the visitor. The healthcheck runs `/api -healthcheck`. The metrics listener binds to loopback inside the container, so it is not reachable from outside; set `METRICS_ADDR=0.0.0.0:9090` only if a scraper on the private network needs it, and never expose it publicly.

New data release: rebuild the artifact, commit, tag, deploy. Roll back by redeploying the previous tag.
