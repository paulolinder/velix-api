<p align="center">
  <img src="internal/ui/web/admin/dist/velix-logo.svg" alt="Velix API" width="72" />
</p>

<h1 align="center">Velix API</h1>

<p align="center">
  API REST open para WhatsApp multi-dispositivo, construída em Go sobre a
  <a href="https://github.com/tulir/whatsmeow">whatsmeow</a>.
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white" alt="Go version" />
  <img src="https://img.shields.io/badge/whatsmeow-multi--device-25D366?logo=whatsapp&logoColor=white" alt="whatsmeow" />
  <img src="https://img.shields.io/badge/Postgres-16-4169E1?logo=postgresql&logoColor=white" alt="Postgres" />
  <img src="https://img.shields.io/badge/Redis-7-DC382D?logo=redis&logoColor=white" alt="Redis" />
</p>

---

## O que é

Velix API é uma API REST não-oficial para o WhatsApp: conecta-se direto ao
WebSocket multi-dispositivo do WhatsApp (via [whatsmeow](https://github.com/tulir/whatsmeow)),
sem navegador headless, sem emulador Android. Suporta múltiplas instâncias
(sessões) por workspace, cada uma com seu próprio número conectado via QR
Code ou pareamento por código.

> [!WARNING]
> Isto usa o protocolo não-oficial do WhatsApp Web. Uso indevido (spam, disparo em massa sem opt-in) viola os Termos de Serviço do WhatsApp e pode banir o número. Use por sua conta e risco. Para volume alto/comercial, considere a WhatsApp Cloud API oficial da Meta.

## Funcionalidades

- **Instâncias multi-dispositivo** — várias sessões WhatsApp por workspace, conexão via QR Code ou pairing code, reconexão automática, "About" e foto de perfil atualizáveis
- **Mensagens** — texto, mídia (imagem/áudio/vídeo/documento/sticker), localização, contato vCard, enquete, reação, edição, revogação, timer de desaparecimento, agendamento, envio em lote, busca e histórico
- **Status (Stories)** — postar atualizações de status em texto (com cor de fundo e fonte), imagem ou vídeo
- **Grupos** — criar, listar, entrar por link, sair, info, gerenciar participantes (add/remove/promote/demote), atualizar nome e descrição, foto do grupo, link de convite (gerar e resetar)
- **Contatos** — checar número no WhatsApp, info e foto de perfil, bloquear/desbloquear, listar bloqueados
- **Presença** — enviar indicador de digitação/gravação/disponível por chat
- **Eventos em tempo real** — WebSocket (`/v1/ws`) para status de conexão, mensagens recebidas, atualizações de presença
- **Integração Chatwoot** — webhook + sincronização de histórico
- **Multi-tenant** — workspaces isolados, autenticação por JWT ou API Key, controle de acesso por papel (admin/developer)
- **Auditoria** — log de ações administrativas
- **Painel admin** — UI web embutida em `/admin`

## Arquitetura

```
Cliente / CRM
     │
     ▼
Velix API (Go)
 ├── internal/api        → handlers HTTP (chi router)
 ├── internal/domain      → regras de negócio (auth, instance, message, chatwoot, media, webhook)
 ├── internal/engine      → interface de engine WhatsApp
 │      └── whatsmeow     → implementação via whatsmeow (protocolo multi-device)
 ├── internal/infra       → Postgres (pgx) + Redis
 └── internal/ui          → painel admin (estático, embutido no binário)
```

A engine WhatsApp fica atrás de uma interface (`internal/engine/interface.go`), então o resto da aplicação não depende diretamente do whatsmeow.

## Requisitos

- [Go](https://go.dev/) 1.25+
- [Docker](https://www.docker.com/) + Docker Compose (para Postgres e Redis)
- PostgreSQL 16 e Redis 7 (via Docker ou instância própria)

## Subindo localmente

```bash
# 1. Clonar
git clone https://github.com/paulolinder/velix-api.git
cd velix-api

# 2. Configurar ambiente
cp .env.example .env
# Edite o .env — para rodar fora do Docker, aponte DATABASE_URL e REDIS_URL
# para localhost em vez de postgres/redis, e defina um JWT_SECRET forte.

# 3. Subir Postgres e Redis
docker compose up -d postgres redis

# 4. Rodar a API (lê variáveis do .env)
set -a && source .env && set +a
make run
# ou: go run ./cmd/api
```

A API sobe em `http://localhost:8080` por padrão (ajuste `HTTP_PORT` no `.env` se a porta já estiver em uso). Verifique a saúde:

```bash
curl http://localhost:8080/health
```

O painel admin fica em `http://localhost:8080/admin` (a raiz `/` redireciona pra lá). A documentação da API (Swagger UI) fica em `/docs`, com o spec OpenAPI cru em `/docs/openapi.yaml`.

### Criando o primeiro usuário

```bash
curl -X POST http://localhost:8080/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "workspace_name": "Minha Empresa",
    "slug": "minha-empresa",
    "email": "admin@meudominio.com",
    "password": "SenhaForte123!"
  }'
```

### Comandos úteis (Makefile)

| Comando | O que faz |
|---|---|
| `make setup` | Primeira execução: cria `.env`, sobe Docker |
| `make run` | Roda a API localmente |
| `make test` | Roda a suíte de testes com race detector |
| `make lint` | Roda o golangci-lint |
| `make docker-up` | Sobe Postgres + Redis |
| `make build` | Compila o binário em `./bin/velix-api` |

## Deploy em produção

Guia completo de deploy (Docker Compose, HTTPS com Nginx/Caddy, backup automático) em [`docs/deploy.md`](docs/deploy.md). Instalação rápida em um servidor limpo:

```bash
curl -fsSL https://raw.githubusercontent.com/paulolinder/velix-api/master/scripts/install.sh | bash
```

A imagem oficial fica no GitHub Container Registry: `ghcr.io/paulolinder/velix-api:latest` (rebuildada a cada push na `master`, multi-arch amd64/arm64).

## Deploy com um clique

| Plataforma | Como instalar |
|---|---|
| [Dokploy](https://dokploy.com) | Template oficial no catálogo — busque por **Velix API** |
| [EasyPanel](https://easypanel.io) | Template oficial no catálogo — busque por **Velix API** |
| [Coolify](https://coolify.io) | [paulolinder/coolify-velix](https://github.com/paulolinder/coolify-velix) |
| [CapRover](https://caprover.com) | [paulolinder/caprover-velix](https://github.com/paulolinder/caprover-velix) — 3rd-party one-click repo |
| [Runtipi](https://runtipi.io) | [paulolinder/runtipi-velix](https://github.com/paulolinder/runtipi-velix) — 3rd-party app store |

## Configuração

Principais variáveis de ambiente (lista completa em [`.env.example`](.env.example) e [`docs/deploy.md`](docs/deploy.md)):

| Variável | Obrigatória | Descrição |
|---|:---:|---|
| `DATABASE_URL` | Sim | Connection string do PostgreSQL |
| `REDIS_URL` | Sim | Connection string do Redis |
| `JWT_SECRET` | Sim | Secret para assinar JWTs (mín. 32 chars) |
| `HTTP_PORT` | Não | Porta HTTP (padrão 8080) |
| `ENGINE_MAX_INSTANCES` | Não | Cap operacional de instâncias por workspace (padrão 200, 0 = sem limite) |
| `CORS_ALLOWED_ORIGINS` | Não | Origens permitidas, separadas por vírgula |

## Contribuindo

Issues e PRs são bem-vindos. Antes de abrir um PR, rode `make test` e `make lint`.
