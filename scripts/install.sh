#!/bin/bash
# ============================================================
# Velix API — Instalador
# Uso: curl -fsSL https://raw.githubusercontent.com/paulolinder/velix-api/master/scripts/install.sh | bash
# Ou com versão específica:
#   curl -fsSL .../install.sh | bash -s v1.2.0
# ============================================================
set -euo pipefail

REPO="paulolinder/velix-api"
INSTALL_DIR="/opt/velix-api"
VERSION="${1:-latest}"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/master"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()    { echo -e "${GREEN}==>${NC} $*"; }
warn()    { echo -e "${YELLOW}[AVISO]${NC} $*"; }
error()   { echo -e "${RED}[ERRO]${NC} $*" >&2; exit 1; }

# ── Pré-requisitos ────────────────────────────────────────────────────────────
[ "$EUID" -eq 0 ] || error "Execute como root: sudo bash install.sh"
command -v curl >/dev/null 2>&1 || apt-get install -y curl -qq

# ── Docker ────────────────────────────────────────────────────────────────────
if ! command -v docker >/dev/null 2>&1; then
    info "Instalando Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
    info "Docker instalado"
else
    info "Docker já instalado: $(docker --version)"
fi

# ── Diretório de instalação ───────────────────────────────────────────────────
info "Criando diretório ${INSTALL_DIR}..."
mkdir -p "${INSTALL_DIR}"
cd "${INSTALL_DIR}"

# ── Baixa arquivos de configuração ───────────────────────────────────────────
info "Baixando docker-compose.prod.yml..."
curl -fsSL "${RAW_BASE}/docker-compose.prod.yml" -o docker-compose.prod.yml

# ── Gera .env se não existir ──────────────────────────────────────────────────
if [ ! -f .env ]; then
    info "Gerando .env com segredos aleatórios..."
    curl -fsSL "${RAW_BASE}/.env.example" -o .env

    DB_PASS=$(openssl rand -hex 20)
    JWT_SECRET=$(openssl rand -hex 32)

    sed -i "s|SENHA_DO_BANCO|${DB_PASS}|g" .env
    sed -i "s|mude-para-um-segredo-forte-de-no-minimo-32-chars|${JWT_SECRET}|" .env
    sed -i "s|APP_ENV=production|APP_ENV=production|" .env

    info ".env gerado com senhas aleatórias"
    warn "Revise o arquivo .env antes de continuar: nano ${INSTALL_DIR}/.env"
else
    warn ".env já existe — mantendo configuração atual"
fi

# ── Define versão ─────────────────────────────────────────────────────────────
if grep -q "^VELIX_VERSION=" .env; then
    sed -i "s|^VELIX_VERSION=.*|VELIX_VERSION=${VERSION}|" .env
else
    echo "VELIX_VERSION=${VERSION}" >> .env
fi

# ── Sobe os serviços ──────────────────────────────────────────────────────────
info "Puxando imagens Docker..."
docker compose -f docker-compose.prod.yml pull

info "Iniciando Velix API..."
docker compose -f docker-compose.prod.yml up -d

# ── Aguarda API ficar saudável ────────────────────────────────────────────────
info "Aguardando API inicializar..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
        PUBLIC_IP=$(curl -s --max-time 3 ifconfig.me 2>/dev/null || echo "SEU_IP")
        echo ""
        echo -e "${GREEN}✅ Velix API instalada com sucesso!${NC}"
        echo "   Painel admin:  http://${PUBLIC_IP}:8080/admin"
        echo "   Health check:  http://${PUBLIC_IP}:8080/health"
        echo "   Logs:          docker compose -f ${INSTALL_DIR}/docker-compose.prod.yml logs -f api"
        echo "   Atualizar:     curl -fsSL ${RAW_BASE}/scripts/update.sh | bash"
        echo ""
        exit 0
    fi
    sleep 2
done

error "API não respondeu em 60s. Veja os logs: docker compose -f ${INSTALL_DIR}/docker-compose.prod.yml logs api"
