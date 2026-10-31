APP_NAME := datechat-api
CONFIG   ?= configs/config.dev.yaml
GO       ?= go

.PHONY: help deps tidy run build migrate migrate-down migrate-status test vet fmt lint docker-up docker-down docker-logs clean

help:
	@echo "make deps            下载依赖"
	@echo "make tidy            go mod tidy"
	@echo "make run             本地启动 API (CONFIG 可覆盖)"
	@echo "make build           编译到 bin/$(APP_NAME)"
	@echo "make migrate         执行未应用的迁移"
	@echo "make migrate-down    回滚最近一次迁移"
	@echo "make migrate-status  查看迁移状态"
	@echo "make test            运行测试"
	@echo "make vet / fmt / lint 静态检查与格式化"
	@echo "make docker-up       启动 MySQL + Redis + Adminer"

deps:
	$(GO) mod download

tidy:
	$(GO) mod tidy

run:
	$(GO) run ./cmd/api -c $(CONFIG)

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o bin/$(APP_NAME) ./cmd/api
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o bin/datechat-migrate ./cmd/migrate

migrate:
	$(GO) run ./cmd/migrate -c $(CONFIG) up

migrate-down:
	$(GO) run ./cmd/migrate -c $(CONFIG) down

migrate-status:
	$(GO) run ./cmd/migrate -c $(CONFIG) status

# 仅用于无 Docker 的本地 MySQL 5.7 环境（会自动替换 8.0 专属排序规则）
migrate-local57:
	bash ../scripts/migrate-local57.sh up

migrate-local57-status:
	bash ../scripts/migrate-local57.sh status

test:
	$(GO) test ./... -count=1 -race

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

lint: vet
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck 未安装，跳过"

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f --tail=100

clean:
	rm -rf bin tmp
