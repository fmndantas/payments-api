# Grafana Log Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make local API and worker `slog` entries searchable in Grafana through Promtail and Loki.

**Architecture:** The API and worker write JSON Lines to separate local files while retaining stderr output. Promtail, launched from a standalone observability Compose file, tails those files and sends them to Loki; Grafana queries Loki through a provisioned datasource.

**Tech Stack:** Go standard library (`log/slog`), GNU Make, Docker Compose, Grafana, Loki, Promtail.

## Global Constraints

- Keep `docker-compose.yml` limited to Postgres; observability lives only in `docker-compose.observability.yml`.
- The application communicates with neither Loki nor Grafana.
- `LOG_LEVEL` and `LOG_FILE` are application environment variables; `LOG_DIR` is an overridable Make variable.
- Write `logs/api.log` and `logs/worker.log` as JSON Lines, and do not commit the `logs/` directory.
- Promtail labels are exactly `service` and `environment`; do not promote event identifiers or errors to labels.
- Initial scope captures `slog` entries only, not Gin access logs or legacy `log` package output.

---

## File Structure

- `internal/configuration.go`: configure the process-wide `slog` default logger and create its log file.
- `cmd/api/main.go`: configure logging before database initialization and close the opened log file on exit.
- `cmd/worker/main.go`: configure logging before database initialization and close the opened log file on exit.
- `Makefile`: assign separate `LOG_FILE` values for API and worker from `LOG_DIR`.
- `.gitignore`: exclude generated local log files.
- `docker-compose.observability.yml`: run only Loki, Promtail, and Grafana.
- `deploy/loki/config.yml`: configure a local, filesystem-backed Loki instance.
- `deploy/promtail/config.yml`: tail the two application files and push entries to Loki.
- `deploy/grafana/provisioning/datasources/loki.yml`: provision Loki as Grafana's default datasource.

## Task 1: Configure JSON File Logging

**Files:**
- Modify: `internal/configuration.go`

**Interfaces:**
- Produces: `func ConfigureLogger(logFilePath, logLevel string, stderr io.Writer) (io.Closer, error)`.
- Consumes: `LOG_FILE` and `LOG_LEVEL` values supplied by the entry points in Task 2.

- [x] **Step 1: Implement `ConfigureLogger`**

Replace `ConfigureLogLevel` in `internal/configuration.go` with the following behavior:

```go
func ConfigureLogger(logFilePath, logLevel string, stderr io.Writer) (io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(logFilePath), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	level := slog.LevelError
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "error":
		level = slog.LevelError
	}

	handler := slog.NewJSONHandler(io.MultiWriter(file, stderr), &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
	return file, nil
}
```

Add the required imports: `fmt`, `io`, `log/slog`, `os`, `path/filepath`, and `strings`.

Manual verification: `internal/configuration.go` contains no `ConfigureLogLevel` function. `ConfigureLogger` creates the parent directory, appends to the supplied file, writes JSON through `io.MultiWriter`, and returns the open file so the caller can close it.

- [x] **Step 2: Format and compile the logging package**

Run:

```sh
gofmt -w internal/configuration.go
go test ./internal
```

Manual verification: formatting produces no further diff on a second `gofmt` run, and the existing `internal` package tests pass. No new logging test file exists.

- [x] **Step 3: Commit the logging implementation**

Run:

```sh
git add internal/configuration.go
git commit -m "feat: write structured logs to files"
```

Manual verification: `git status --short` is empty after the commit.

## Task 2: Supply Per-Service Log Files from Entry Points

**Files:**
- Modify: `Makefile`
- Modify: `cmd/api/main.go`
- Modify: `cmd/worker/main.go`
- Create: `.gitignore`

**Interfaces:**
- Consumes: `internal.ConfigureLogger(logFilePath, logLevel, stderr)` from Task 1.
- Produces: API logs at `logs/api.log` and worker logs at `logs/worker.log` by default.

- [x] **Step 1: Update the Make targets**

Set the Makefile contents to:

```make
LOG_LEVEL ?= info
LOG_DIR ?= logs

api:
	LOG_FILE=$(LOG_DIR)/api.log LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/api/main.go

worker:
	LOG_FILE=$(LOG_DIR)/worker.log LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/worker/main.go

tests:
	go test ./...
```

Manual verification: the file defines only `LOG_LEVEL` and `LOG_DIR` variables, and both application targets set their own `LOG_FILE` inline environment variable.

- [x] **Step 2: Inspect the commands Make will run**

Run:

```sh
make -n api
make -n worker
make -n api LOG_DIR=/tmp/payments-logs LOG_LEVEL=debug
```

Manual verification: the first two commands show distinct `LOG_FILE=logs/api.log` and `LOG_FILE=logs/worker.log`; the third shows `/tmp/payments-logs/api.log` and `LOG_LEVEL=debug`.

- [x] **Step 3: Configure logging before dependencies in both binaries**

In `cmd/api/main.go` and `cmd/worker/main.go`, add `os` to imports. At the beginning of each `main`, before `dependencies.Initialize`, call:

```go
logFile, err := internal.ConfigureLogger(os.Getenv("LOG_FILE"), os.Getenv("LOG_LEVEL"), os.Stderr)
if err != nil {
	log.Fatal(err)
}
defer logFile.Close()
```

Remove the old `internal.ConfigureLogLevel()` calls. Also remove the unconditional `slog.SetLogLoggerLevel(slog.LevelDebug)` from `cmd/api/main.go`; it would override `LOG_LEVEL`.

Manual verification: in both `main` functions, `ConfigureLogger` runs before `dependencies.Initialize`; `cmd/api/main.go` no longer imports `log/slog`.

- [x] **Step 4: Format and compile the changed application code**

Run:

```sh
gofmt -w internal/configuration.go cmd/api/main.go cmd/worker/main.go
go test ./cmd/api ./cmd/worker ./internal
```

Manual verification: formatting produces no further diff on a second `gofmt` run, and all selected package tests pass.

- [x] **Step 5: Ignore generated logs**

Create `.gitignore` with:

```gitignore
logs/
```

Manual verification: run `mkdir -p logs && touch logs/api.log && git status --short`; neither file appears. Remove the empty local `logs/` directory afterward with `rmdir logs`.

- [x] **Step 6: Run each process and inspect its log file**

Start dependencies in one terminal:

```sh
docker compose up -d
```

In a second terminal, run `make api`, then request `GET http://localhost:8080/health`. Stop it with `Ctrl-C`. Run `make worker` for at least six seconds, then stop it with `Ctrl-C`.

Manual verification: `logs/api.log` contains JSON objects and the API startup/request-related `slog` entries; `logs/worker.log` contains JSON objects and worker/database `slog` entries. Terminal output contains the same JSON entries.

- [x] **Step 7: Commit the entry-point and Make changes**

Run:

```sh
git add Makefile cmd/api/main.go cmd/worker/main.go .gitignore
git commit -m "feat: separate API and worker log files"
```

Manual verification: `git status --short` is empty after the commit.

## Task 3: Add the Standalone Observability Stack

**Files:**
- Create: `docker-compose.observability.yml`
- Create: `deploy/loki/config.yml`
- Create: `deploy/promtail/config.yml`
- Create: `deploy/grafana/provisioning/datasources/loki.yml`

**Interfaces:**
- Consumes: JSON Lines at `./logs/api.log` and `./logs/worker.log` from Task 2.
- Produces: Grafana at `http://localhost:3000`, with `Loki` configured as its default datasource.

- [x] **Step 1: Create the Loki configuration**

Create `deploy/loki/config.yml`:

```yaml
auth_enabled: false

server:
  http_listen_port: 3100

common:
  path_prefix: /loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
  storage:
    filesystem:
      chunks_directory: /loki/chunks
      rules_directory: /loki/rules

schema_config:
  configs:
    - from: 2024-01-01
      store: tsdb
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h

compactor:
  working_directory: /loki/compactor
  retention_enabled: true

limits_config:
  retention_period: 168h

analytics:
  reporting_enabled: false
```

Manual verification: `docker run --rm -v "$PWD/deploy/loki/config.yml:/etc/loki/config.yml:ro" grafana/loki:3.4.2 -config.file=/etc/loki/config.yml -verify-config=true` exits with status 0.

- [x] **Step 2: Create the Promtail configuration**

Create `deploy/promtail/config.yml`:

```yaml
server:
  http_listen_port: 9080
  grpc_listen_port: 0

positions:
  filename: /tmp/positions/positions.yaml

clients:
  - url: http://loki:3100/loki/api/v1/push

scrape_configs:
  - job_name: payments-api
    static_configs:
      - targets: [localhost]
        labels:
          service: api
          environment: dev
          __path__: /var/log/payments/api.log
    pipeline_stages:
      - json:
          expressions:
            level:
            msg:

  - job_name: payments-worker
    static_configs:
      - targets: [localhost]
        labels:
          service: worker
          environment: dev
          __path__: /var/log/payments/worker.log
    pipeline_stages:
      - json:
          expressions:
            level:
            msg:
```

Manual verification: confirm only `service` and `environment` are labels. `level`, `msg`, IDs, tokens, and errors remain JSON fields, not labels.

- [x] **Step 3: Provision Grafana's Loki datasource**

Create `deploy/grafana/provisioning/datasources/loki.yml`:

```yaml
apiVersion: 1

datasources:
  - name: Loki
    type: loki
    access: proxy
    url: http://loki:3100
    isDefault: true
    editable: true
```

Manual verification: verify the URL uses the Compose service name `loki`, not `localhost`.

- [x] **Step 4: Create the observability-only Compose file**

Create `docker-compose.observability.yml`:

```yaml
services:
  loki:
    image: grafana/loki:3.4.2
    command: -config.file=/etc/loki/config.yml
    volumes:
      - ./deploy/loki/config.yml:/etc/loki/config.yml:ro
      - loki_data:/loki

  promtail:
    image: grafana/promtail:3.4.2
    command: -config.file=/etc/promtail/config.yml
    depends_on:
      - loki
    volumes:
      - ./deploy/promtail/config.yml:/etc/promtail/config.yml:ro
      - ./logs:/var/log/payments:ro
      - promtail_positions:/tmp/positions

  grafana:
    image: grafana/grafana:12.1.1
    depends_on:
      - loki
    ports:
      - "3000:3000"
    volumes:
      - grafana_data:/var/lib/grafana
      - ./deploy/grafana/provisioning:/etc/grafana/provisioning:ro

volumes:
  grafana_data:
  loki_data:
  promtail_positions:
```

Manual verification: `docker-compose.yml` remains unchanged and contains only the Postgres service.

- [x] **Step 5: Validate and start the Compose stack**

Run:

```sh
docker compose -f docker-compose.observability.yml config
docker compose -f docker-compose.observability.yml up -d
docker compose -f docker-compose.observability.yml ps
```

Manual verification: `config` exits successfully, and `ps` lists exactly `loki`, `promtail`, and `grafana` as running. Grafana is reachable at `http://localhost:3000`.

- [x] **Step 6: Commit the observability configuration**

Run:

```sh
git add docker-compose.observability.yml deploy/loki/config.yml deploy/promtail/config.yml deploy/grafana/provisioning/datasources/loki.yml
git commit -m "feat: add local Grafana log stack"
```

Manual verification: `git status --short` is empty after the commit.

## Task 4: Verify End-to-End Log Visibility and Restart Behavior

**Files:**
- No source changes expected.

**Interfaces:**
- Consumes: running observability services from Task 3 and generated JSON log files from Task 2.
- Produces: confirmed Grafana queries and persisted Promtail read position.

- [x] **Step 1: Generate a known API log entry**

Ensure the observability stack is running, then start the API with `make api`. Request the health endpoint and stop the API:

```sh
curl --fail http://localhost:8080/health
```

Manual verification: `logs/api.log` contains a valid JSON line with a `level` and `msg` field created after the observability stack started.

- [x] **Step 2: Confirm API logs in Grafana Explore**

Open `http://localhost:3000`, log in with Grafana's initial local credentials (`admin` / `admin`), then open **Explore** and select the `Loki` datasource. Run:

```logql
{service="api", environment="dev"}
```

Manual verification: returned entries match lines in `logs/api.log`, including the message and timestamp.

- [x] **Step 3: Confirm worker logs and JSON field queries**

Run `make worker` for at least six seconds, then stop it. In Grafana Explore run:

```logql
{service="worker", environment="dev"}
```

Then query parsed JSON fields:

```logql
{service="worker"} | json | level="ERROR"
```

Manual verification: the first query returns worker lines only. The second query either returns matching error entries or an empty result without a parser error; generate a known worker error only if the existing behavior provides one safely.

- [xx] **Step 4: Verify Promtail restart position persistence**

Record the most recent API log line timestamp in Grafana. Restart Promtail, then create exactly one new API request:

```sh
docker compose -f docker-compose.observability.yml restart promtail
curl --fail http://localhost:8080/health
```

Manual verification: Grafana receives the new line after the restart and does not show a second copy of all earlier API lines. Confirm the named `promtail_positions` volume still exists with `docker volume ls`.

- [x] **Step 5: Run the full automated regression suite**

Run:

```sh
go test ./...
```

Manual verification: all Go tests pass. If Docker-backed tests require a running Docker daemon, start it and rerun rather than skipping the suite.

- [x] **Step 6: Commit the verified implementation state**

If Task 4 required no source changes, no commit is needed. Otherwise commit only the explicit corrective files after rerunning their corresponding verification commands.

Manual verification: `git status --short` is empty and `git log --oneline -4` shows the three implementation commits from Tasks 1-3.
