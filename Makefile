.PHONY: help backend-fmt backend-test backend-race backend-build web-install web-test web-build check compose-up compose-down migrate-up migrate-down

GOCACHE_DIR ?= /tmp/feedvelis-go-cache

help:
	@echo "make check         格式化并验证后端和 Web"
	@echo "make compose-up    构建并启动本地完整环境"
	@echo "make compose-down  停止本地环境"
	@echo "make migrate-up    对本地 PostgreSQL 执行迁移"

backend-fmt:
	cd backend && gofmt -w $$(find . -name '*.go' -type f)

backend-test:
	cd backend && GOCACHE=$(GOCACHE_DIR) go test ./...

backend-race:
	cd backend && GOCACHE=$(GOCACHE_DIR) go test -race -count=1 ./...

backend-build:
	cd backend && GOCACHE=$(GOCACHE_DIR) go build ./cmd/...

web-install:
	cd web && npm ci

web-test:
	cd web && npm test

web-build:
	cd web && npm run build

check: backend-fmt backend-test backend-race backend-build web-test web-build

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down

migrate-up:
	cd backend && GOCACHE=$(GOCACHE_DIR) go run ./cmd/velis-migrate -config configs/config.example.yaml up

migrate-down:
	cd backend && GOCACHE=$(GOCACHE_DIR) go run ./cmd/velis-migrate -config configs/config.example.yaml -steps 1 down
