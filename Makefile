LOG_LEVEL ?= info

api:
	LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/api/main.go

worker:
	LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/worker/main.go

tests:
	go test ./...
