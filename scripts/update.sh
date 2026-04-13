#!/bin/bash
# ============================================================
# Velix API — Atualizador
# Uso: curl -fsSL .../update.sh | bash -s v1.2.0
#      curl -fsSL .../update.sh | bash          (usa latest)
# ============================================================
set -euo pipefail

INSTALL_DIR="/opt/velix-api"
NEW_VERSION="${1:-latest}"
COMPOSE="docker compose -f ${INSTALL_DIR}/docker-compose.prod.yml"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}==>${NC} $*"; }
warn()  { echo -e "${YELLOW}[AVISO]${NC} $*"; }
error() { echo -e "${RED}[ERRO]${NC} $*" >&2; exit 1; }

[ "$EUID" -eq 0 ] || error "Execute como root: sudo bash update.sh"
[ -d "$INSTALL_DIR" ] || error "Velix API não encontrada em ${INSTALL_DIR}. Execute o install.sh primeiro."

cd "$INSTALL_DIR"

# ── Versão atual ──────────────────────────────────────────────────────────────
CURRENT_VERSION=$(grep "^VELIX_VERSION=" .env 2>/dev/null | cut -d= -f2 || echo "desconhecida")
info "Versão atual: ${CURRENT_VERSION} → Nova versão: ${NEW_VERSION}"

# ── Backup antes de atualizar ─────────────────────────────────────────────────
cp .env .env.backup
info "Backup do .env salvo em .env.backup"

# Database backup (if backup script exists).
if [ -x "${INSTALL_DIR}/scripts/backup.sh" ]; then
    info "Executando backup do banco de dados..."
    "${INSTALL_DIR}/scripts/backup.sh" || warn "Backup falhou — continuando update"
fi

# Backup WhatsApp sessions (SQLite files).
if [ -d "${INSTALL_DIR}/data/instances" ]; then
    SESSIONS_BACKUP="${INSTALL_DIR}/backups/sessions_pre_update_$(date +%Y%m%d_%H%M%S).tar.gz"
    mkdir -p "${INSTALL_DIR}/backups"
    tar czf "$SESSIONS_BACKUP" -C "${INSTALL_DIR}" data/instances/ 2>/dev/null && \
        info "Backup das sessões WhatsApp: $SESSIONS_BACKUP" || \
        warn "Backup das sessões falhou — continuando"
fi

# ── Atualiza versão no .env ───────────────────────────────────────────────────
if grep -q "^VELIX_VERSION=" .env; then
    sed -i "s|^VELIX_VERSION=.*|VELIX_VERSION=${NEW_VERSION}|" .env
else
    echo "VELIX_VERSION=${NEW_VERSION}" >> .env
fi

# ── Puxa nova imagem ──────────────────────────────────────────────────────────
info "Puxando imagem ${NEW_VERSION}..."
$COMPOSE pull api

# ── Reinicia só o container da API (Postgres e Redis ficam vivos) ─────────────
info "Reiniciando API..."
$COMPOSE up -d --no-deps api

# ── Aguarda healthcheck ───────────────────────────────────────────────────────
info "Aguardando API ficar saudável..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
        echo ""
        echo -e "${GREEN}✅ Velix API atualizada para ${NEW_VERSION}!${NC}"
        echo "   Logs: $COMPOSE logs -f api"
        rm -f .env.backup
        exit 0
    fi
    sleep 2
done

# ── Rollback automático se healthcheck falhar ─────────────────────────────────
warn "Healthcheck falhou — revertendo para ${CURRENT_VERSION}..."
cp .env.backup .env
$COMPOSE pull api
$COMPOSE up -d --no-deps api

sleep 5
if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
    warn "Rollback concluído — versão ${CURRENT_VERSION} restaurada"
else
    error "Rollback também falhou. Verifique os logs: $COMPOSE logs api"
fi
exit 1
