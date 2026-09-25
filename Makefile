.PHONY: help backend-fmt backend-test backend-race backend-build integration-postgres integration-rabbitmq integration-opensearch integration-search integration-async integration-all web-install web-lint web-test web-build check compose-up compose-down migrate-up migrate-down

GOCACHE_DIR ?= /tmp/feedvelis-go-cache

help:
	@echo "make check         格式化并验证后端和 Web"
	@echo "make compose-up    构建并启动本地完整环境"
	@echo "make compose-down  停止本地环境"
	@echo "make migrate-up    对本地 PostgreSQL 执行迁移"
	@echo "make integration-postgres 运行 PostgreSQL 真实依赖测试（OpenSearch 用例未配置时明确跳过）"
	@echo "make integration-rabbitmq 运行 RabbitMQ 真实依赖测试"
	@echo "make integration-opensearch 运行 OpenSearch 模板/Bulk/别名及 BM25/PIT 真实依赖测试"
	@echo "make integration-search 运行投影重建及搜索事实源复核测试（同时需要 PostgreSQL 与 OpenSearch）"
	@echo "make integration-async 运行 PostgreSQL + RabbitMQ 可靠异步真实依赖测试"
	@echo "make integration-all  运行全部真实依赖测试"

backend-fmt:
	cd backend && gofmt -w $$(find . -name '*.go' -type f)

backend-test:
	cd backend && GOCACHE=$(GOCACHE_DIR) go test ./...

backend-race:
	cd backend && GOCACHE=$(GOCACHE_DIR) go test -race -count=1 ./...

backend-build:
	cd backend && GOCACHE=$(GOCACHE_DIR) go build ./cmd/...

integration-postgres:
	@test -n "$(VELIS_TEST_DATABASE_URL)" || (echo "未设置 VELIS_TEST_DATABASE_URL（必须指向 _test 数据库）" && exit 2)
	cd backend && GOCACHE=$(GOCACHE_DIR) go test -count=1 -v ./test/integration

integration-rabbitmq:
	@test -n "$(VELIS_TEST_RABBITMQ_URL)" || (echo "未设置 VELIS_TEST_RABBITMQ_URL" && exit 2)
	cd backend && GOCACHE=$(GOCACHE_DIR) go test -count=1 -v ./internal/infrastructure/messaging/rabbitmq

# 搜索投影的真实依赖测试必须显式指向可用的 OpenSearch：未配置时明确失败，
# 不允许把 skip 当成通过。
integration-opensearch:
	@test -n "$(VELIS_TEST_OPENSEARCH_URL)" || (echo "未设置 VELIS_TEST_OPENSEARCH_URL（必须指向可用的 OpenSearch 3.x）" && exit 2)
	cd backend && GOCACHE=$(GOCACHE_DIR) go test -count=1 -v ./internal/infrastructure/search/opensearch

integration-search:
	@test -n "$(VELIS_TEST_DATABASE_URL)" || (echo "未设置 VELIS_TEST_DATABASE_URL（必须指向 _test 数据库）" && exit 2)
	@test -n "$(VELIS_TEST_OPENSEARCH_URL)" || (echo "未设置 VELIS_TEST_OPENSEARCH_URL（必须指向可用的 OpenSearch 3.x）" && exit 2)
	cd backend && GOCACHE=$(GOCACHE_DIR) go test -count=1 -v -run 'TestSearchProjection|TestSearchIndexRebuild|TestSearchRebuild|TestArticleSearch' ./test/integration

integration-async: integration-postgres integration-rabbitmq

integration-all: integration-postgres integration-rabbitmq integration-opensearch integration-search

web-install:
	cd web && npm ci

web-test:
	cd web && npm test

web-lint:
	cd web && npm run lint

web-build:
	cd web && npm run build

check: backend-fmt backend-test backend-race backend-build web-lint web-test web-build

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down

migrate-up:
	cd backend && GOCACHE=$(GOCACHE_DIR) go run ./cmd/velis-migrate -config configs/config.example.yaml up

migrate-down:
	cd backend && GOCACHE=$(GOCACHE_DIR) go run ./cmd/velis-migrate -config configs/config.example.yaml -steps 1 down
