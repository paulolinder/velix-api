## ============================================================
## Velix API — Makefile
## Uso: make <target>
## ============================================================

BINARY     := velix-api
MAIN       := ./cmd/api
BUILD_DIR  := ./bin
GO         := go
IMAGE      := ghcr.io/paulolinder/velix-api

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.DEFAULT_GOAL := help

# ── Ajuda ─────────────────────────────────────────────────────────────────────
.PHONY: help
help: ## Mostra os targets disponíveis
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ── Desenvolvimento ───────────────────────────────────────────────────────────
.PHONY: run
run: ## Roda a API localmente (lê o .env)
	@$(GO) run $(MAIN)

.PHONY: build
build: ## Compila o binário em ./bin/velix-api
	@mkdir -p $(BUILD_DIR)
	@$(GO) build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY) $(MAIN)
	@echo "Compilado: $(BUILD_DIR)/$(BINARY) ($(VERSION))"

.PHONY: clean
clean: ## Remove artefatos de build e relatórios de cobertura
	@rm -rf $(BUILD_DIR) coverage.out coverage.html

# ── Testes ────────────────────────────────────────────────────────────────────
.PHONY: test
test: ## Roda todos os testes com race detector
	@$(GO) test ./... -race -count=1 -timeout=60s

.PHONY: test-cover
test-cover: ## Testes com relatório de cobertura HTML
	@$(GO) test ./... -coverprofile=coverage.out
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Relatório: coverage.html"

# ── Qualidade de Código ───────────────────────────────────────────────────────
.PHONY: lint
lint: ## Roda golangci-lint
	@golangci-lint run ./...

.PHONY: fmt
fmt: ## Formata todos os arquivos Go
	@$(GO) fmt ./...

.PHONY: vet
vet: ## Roda go vet
	@$(GO) vet ./...

.PHONY: tidy
tidy: ## Atualiza go.mod e go.sum
	@$(GO) mod tidy

# ── Docker (Dev) ──────────────────────────────────────────────────────────────
.PHONY: docker-up
docker-up: ## Sobe Postgres e Redis em background
	@docker compose up -d postgres redis
	@echo "Aguardando serviços ficarem saudáveis..."
	@docker compose ps

.PHONY: docker-up-dev
docker-up-dev: ## Sobe tudo incluindo pgAdmin (profile=dev)
	@docker compose --profile dev up -d

.PHONY: docker-down
docker-down: ## Para todos os serviços Docker
	@docker compose down

.PHONY: docker-logs
docker-logs: ## Acompanha logs dos containers
	@docker compose logs -f

# ── Docker (Produção) ─────────────────────────────────────────────────────────
.PHONY: docker-build
docker-build: ## Builda a imagem Docker com tag de versão
	@docker build --build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(VERSION) \
		-t $(IMAGE):latest \
		.
	@echo "Imagem: $(IMAGE):$(VERSION)"

.PHONY: docker-push
docker-push: docker-build ## Pusha a imagem para o GHCR
	@docker push $(IMAGE):$(VERSION)
	@docker push $(IMAGE):latest
	@echo "Publicado: $(IMAGE):$(VERSION)"

.PHONY: prod-up
prod-up: ## Sobe stack de produção (puxa imagem do registry)
	@docker compose -f docker-compose.prod.yml up -d
	@echo "Stack de produção no ar"

.PHONY: prod-down
prod-down: ## Para a stack de produção
	@docker compose -f docker-compose.prod.yml down

.PHONY: prod-logs
prod-logs: ## Acompanha logs da API em produção
	@docker compose -f docker-compose.prod.yml logs -f api

.PHONY: prod-ps
prod-ps: ## Status dos containers de produção
	@docker compose -f docker-compose.prod.yml ps

.PHONY: prod-restart
prod-restart: ## Reinicia só o container da API (sem tocar banco/redis)
	@docker compose -f docker-compose.prod.yml up -d --no-deps api

# ── Setup Inicial ─────────────────────────────────────────────────────────────
.PHONY: setup
setup: ## Primeira execução: copia .env, sobe Docker, aplica migrations
	@[ -f .env ] || (cp .env.example .env && echo ".env criado — edite os valores antes de continuar")
	@$(MAKE) docker-up
	@sleep 3
	@echo "✓ Setup completo. Rode: make run"

# ── Release ───────────────────────────────────────────────────────────────────
.PHONY: release
release: ## Cria e pusha uma nova tag de versão: make release v=1.2.0
	@[ -n "$(v)" ] || (echo "Uso: make release v=1.2.0" && exit 1)
	@git tag v$(v)
	@git push origin v$(v)
	@echo "Tag v$(v) criada — GitHub Actions vai buildar e publicar a imagem"
