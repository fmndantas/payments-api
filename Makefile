LOG_LEVEL ?= info
LOG_DIR ?= logs

db-up:
	docker-compose -f ./docker-compose.yml up -d

db-down:
	docker-compose -f ./docker-compose.yml down

psql:
	docker exec -it payments-db-1 psql -U postgres -d payments

enter-db:
	docker exec -it payments-db-1 bash

api:
	LOG_FILE=$(LOG_DIR)/api.log LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/api/main.go

worker:
	LOG_FILE=$(LOG_DIR)/worker.log LOG_LEVEL=$(LOG_LEVEL) go run ./cmd/worker/main.go

tests:
	go test ./...
