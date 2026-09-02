LOG_LEVEL ?= info
LOG_DIR ?= logs

api:
	LOG_FILE=$(LOG_DIR)/api.log LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/api/main.go

worker:
	LOG_FILE=$(LOG_DIR)/worker.log LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/worker/main.go

tests:
	go test ./...
