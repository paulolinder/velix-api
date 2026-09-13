# Velix API — Guia de Deploy em Produção

> **AVISO IMPORTANTE:** O diretório `./data` contém as sessões WhatsApp (SQLite) e mídias.
> **NUNCA** apague este diretório ou rode `docker compose down -v`.
> Se perder o `./data`, todos os clientes precisam escanear QR code novamente.

## Requisitos mínimos

| Recurso | Mínimo | Recomendado (50+ instâncias) |
|---------|--------|------------------------------|
| CPU     | 1 vCPU | 2 vCPU |
| RAM     | 1 GB   | 4 GB |
| Disco   | 10 GB  | 40 GB SSD |
| SO      | Ubuntu 22.04+ / Debian 12+ | Ubuntu 24.04 |

## Instalação rápida

```bash
curl -fsSL https://raw.githubusercontent.com/paulolinder/velix-api/master/scripts/install.sh | bash
```

## Instalação manual

### 1. Instalar Docker

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
# Relogar para aplicar o grupo
```

### 2. Criar diretório

```bash
sudo mkdir -p /opt/velix-api
cd /opt/velix-api
```

### 3. Baixar docker-compose

```bash
curl -fsSL https://raw.githubusercontent.com/paulolinder/velix-api/master/docker-compose.prod.yml -o docker-compose.yml
```

### 4. Configurar .env

```bash
cat > .env << 'EOF'
# ── Aplicação ────────────────────────────────
APP_ENV=production
LOG_LEVEL=info

# ── HTTP ─────────────────────────────────────
HTTP_HOST=0.0.0.0
HTTP_PORT=8080

# ── Banco de dados ───────────────────────────
# Gerado automaticamente pelo install.sh ou defina manualmente:
DATABASE_URL=postgres://velix:SUA_SENHA_AQUI@postgres:5432/velix?sslmode=disable
POSTGRES_PASSWORD=SUA_SENHA_AQUI

# ── Redis ────────────────────────────────────
REDIS_URL=redis://redis:6379
REDIS_PASSWORD=SUA_SENHA_REDIS

# ── Autenticação ─────────────────────────────
# IMPORTANTE: gere com: openssl rand -base64 48
JWT_SECRET=GERE_UMA_STRING_ALEATORIA_DE_48_CHARS

# ── Mídia ────────────────────────────────────
MEDIA_STORAGE_PATH=/data/media
MEDIA_MAX_FILE_SIZE=67108864

# ── Engine ───────────────────────────────────
ENGINE_STORE_PATH=/data/instances
ENGINE_MAX_INSTANCES=200
ENGINE_AUTO_RECONNECT=true

# ── Integrações (opcional) ───────────────────
# CHATWOOT_WEBHOOK_SECRET=seu_segredo_aqui
# CORS_ALLOWED_ORIGINS=https://seudominio.com

# ── Backup ───────────────────────────────────
BACKUP_DIR=/opt/velix-api/backups
BACKUP_RETAIN=30
EOF
```

Edite os valores marcados com `SUA_SENHA` e `GERE_UMA_STRING`.

### 5. Subir os serviços

```bash
docker compose up -d
```

### 6. Verificar saúde

```bash
curl http://localhost:8080/health
# Deve retornar: {"status":"ok", ...}
```

### 7. Criar primeiro usuário

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

---

## Configurar HTTPS (obrigatório em produção)

### Opção A: Nginx + Let's Encrypt (recomendado)

```bash
# 1. Instalar Nginx e Certbot
sudo apt install -y nginx certbot python3-certbot-nginx

# 2. Criar config do Nginx
sudo cat > /etc/nginx/sites-available/velix << 'NGINX'
server {
    listen 80;
    server_name api.seudominio.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket support
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # SSE support (QR code streaming)
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
    }

    # Uploads até 64 MB
    client_max_body_size 64m;
}
NGINX

# 3. Ativar e testar
sudo ln -sf /etc/nginx/sites-available/velix /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx

# 4. Obter certificado SSL (substitua pelo seu domínio)
sudo certbot --nginx -d api.seudominio.com

# 5. Renovação automática (certbot já configura cron)
sudo certbot renew --dry-run
```

### Opção B: Caddy (mais simples)

```bash
# 1. Instalar Caddy
sudo apt install -y caddy

# 2. Configurar
sudo cat > /etc/caddy/Caddyfile << 'CADDY'
api.seudominio.com {
    reverse_proxy localhost:8080
}
CADDY

# 3. Reiniciar (SSL automático)
sudo systemctl restart caddy
```

Após configurar HTTPS, atualize o `.env`:
```
CORS_ALLOWED_ORIGINS=https://api.seudominio.com
```

---

## Backup automático

```bash
# Tornar executável
chmod +x /opt/velix-api/scripts/backup.sh

# Testar manualmente
/opt/velix-api/scripts/backup.sh

# Agendar backup diário às 3h da manhã
(crontab -l 2>/dev/null; echo "0 3 * * * /opt/velix-api/scripts/backup.sh >> /var/log/velix-backup.log 2>&1") | crontab -
```

### Restaurar backup

```bash
/opt/velix-api/scripts/restore.sh backups/velix_backup_20260412_030000.sql.gz
```

---

## Atualizar

```bash
cd /opt/velix-api
docker compose pull
docker compose up -d
```

---

## Variáveis de ambiente

| Variável | Obrigatória | Default | Descrição |
|----------|:-----------:|---------|-----------|
| `DATABASE_URL` | Sim | — | Connection string PostgreSQL |
| `REDIS_URL` | Sim | — | Connection string Redis |
| `JWT_SECRET` | Sim | — | Secret para assinar JWTs (min 32 chars) |
| `HTTP_PORT` | Não | 8080 | Porta HTTP |
| `APP_ENV` | Não | development | development / production |
| `LOG_LEVEL` | Não | info | debug / info / warn / error |
| `ENGINE_MAX_INSTANCES` | Não | 200 | Cap operacional de instâncias por workspace (0 = sem limite) |
| `ENGINE_STORE_PATH` | Não | ./data/instances | Caminho dos SQLite do WhatsApp |
| `MEDIA_STORAGE_PATH` | Não | ./data/media | Caminho das mídias recebidas |
| `MEDIA_MAX_FILE_SIZE` | Não | 64 MB | Tamanho máximo de upload |
| `CORS_ALLOWED_ORIGINS` | Não | * | Domínios permitidos (separados por vírgula) |
| `CHATWOOT_WEBHOOK_SECRET` | Não | — | Secret para validar webhooks do Chatwoot |
| `REDIS_PASSWORD` | Não | — | Senha do Redis |

---

## Troubleshooting

### "Instance not connecting"
```bash
# Ver logs da API
docker compose logs -f api

# Ver status da instância
curl -H "Authorization: Bearer TOKEN" http://localhost:8080/v1/instances/ID/status
```

### "Webhook not delivering"
```bash
# Verificar se a URL é acessível a partir do servidor
curl -v URL_DO_WEBHOOK

# Ver logs de webhook
docker compose logs api | grep webhook
```

### "Disk full"
```bash
# Verificar uso
du -sh /opt/velix-api/data/*

# Limpar mídias antigas manualmente (>7 dias)
find /opt/velix-api/data/media -type f -mtime +7 -delete
```

### "Database connection refused"
```bash
# Verificar se PostgreSQL está rodando
docker compose ps postgres

# Verificar logs do banco
docker compose logs postgres
```
