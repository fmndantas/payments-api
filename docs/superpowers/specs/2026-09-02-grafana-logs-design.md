# Grafana Log Visibility Design

## Goal

Make local API and worker logs searchable in Grafana without coupling the Go application to Loki's HTTP API. The local stack must run through Docker Compose while the application continues to run with `make api` and `make worker`.

## Scope

Included:

- JSON log output written to local files by the Go application.
- Loki, Promtail, and Grafana services in the existing Compose stack.
- Promtail collection of API and worker logs with stable labels.
- Automatic provisioning of Loki as Grafana's data source.

Excluded:

- Containerizing the API or worker.
- Hosted Grafana/Loki, Kubernetes, log-based alerting, or production retention policies.
- Capturing Gin access logs or legacy `log` package output. The initial integration targets `slog` entries.

## Architecture

```
make api      -> logs/api.log    -> Promtail -> Loki -> Grafana
make worker   -> logs/worker.log -> Promtail -> Loki -> Grafana
```

The application owns local log-file creation. Promtail is the log collector and shipper: it tails the files, applies labels, and sends batches to Loki. Loki is the log database: it receives, stores, indexes, and serves log entries to Grafana. Promtail is the only component that communicates with Loki, preserving the application's independence from the observability backend.

## Application Logging

`internal.ConfigureLogLevel` will become the single logging configuration entry point.

- Read `LOG_LEVEL`, retaining the existing `debug`, `info`, and `error` behavior.
- Read `LOG_FILE`; each Make target supplies `logs/api.log` or `logs/worker.log`.
- Create the parent log directory when needed.
- Set the default `slog` logger to a JSON handler writing to both the selected file and stderr. Local terminal visibility is therefore retained while Promtail consumes the file.
- Each entry is one JSON object per line, allowing Promtail and Loki to parse structured fields without regex parsing.

`Makefile` will expose the existing `LOG_LEVEL` and add an overridable `LOG_DIR` defaulting to `logs`. The API and worker targets pass the correct `LOG_FILE` values.

The separate files are intentional: they produce the stable `service=api` and `service=worker` labels without relying on message content or a process-specific code change.

## Docker Compose Services

The existing `docker-compose.yml` will retain Postgres and add:

- **Loki:** local single-node log store with persistent local storage and a development-appropriate retention configuration.
- **Promtail:** mounts `./logs` read-only and its position file persistently; tails both JSON log files and pushes batches to Loki.
- **Grafana:** exposed on port 3000 with its data directory persisted and the Loki data source provisioned at startup.

All services use Compose's default network. Loki's HTTP endpoint is internal to the Compose network; Grafana and Promtail reach it by the `loki` service name.

## Promtail Labels and Parsing

Promtail will define one scrape job per file:

| File | Labels |
| --- | --- |
| `logs/api.log` | `service=api`, `environment=dev` |
| `logs/worker.log` | `service=worker`, `environment=dev` |

Each job parses JSON so application fields such as `level`, `msg`, `id`, `token`, and `error` remain queryable from Grafana. Only low-cardinality fields are labels; event-specific fields stay in the log payload to avoid Loki label-cardinality problems.

Promtail's persisted positions file prevents it from rereading the complete files after a restart.

## Grafana Usage

Provision Loki as the default data source. In Grafana Explore, expected initial queries are:

```logql
{service="api"}
{service="worker", environment="dev"}
{service="worker"} | json | level="ERROR"
```

The initial implementation provisions the data source only. Users can create dashboards in Grafana once they have real log traffic; no dashboard JSON is required for this scope.

## Error Handling

- If the configured log file cannot be opened, application startup fails with a clear error rather than silently losing logs.
- Promtail retries Loki delivery according to its client configuration; logs remain in local files while Loki is unavailable.
- Grafana availability does not affect application logging or Promtail collection.

## Verification

1. Run `docker compose up -d` and confirm Postgres, Loki, Promtail, and Grafana are healthy.
2. Run `make api`, request `GET /health`, and confirm JSON lines appear in `logs/api.log`.
3. Run `make worker` and confirm JSON lines appear in `logs/worker.log`.
4. Open Grafana at `http://localhost:3000`, select Explore, and query `{service="api"}` and `{service="worker"}`.
5. Restart Promtail and confirm it resumes from its saved position without duplicating previously ingested log lines.

## Files Affected

- Modify: `internal/configuration.go`, `Makefile`, `docker-compose.yml`.
- Add: Loki, Promtail, and Grafana provisioning configuration under `deploy/`.
- Add: `.gitignore` entry for `logs/`.
