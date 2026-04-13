#!/usr/bin/env bash
# Velix API — Restore PostgreSQL from backup.
# Usage: ./scripts/restore.sh backups/velix_backup_20260412_030000.sql.gz
#
set -euo pipefail

BACKUP_FILE="${1:-}"

if [ -z "$BACKUP_FILE" ] || [ ! -f "$BACKUP_FILE" ]; then
  echo "Usage: $0 <backup_file.sql.gz>" >&2
  echo "Available backups:" >&2
  ls -lh backups/velix_backup_*.sql.gz 2>/dev/null || echo "  (none found)"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  source "$PROJECT_DIR/.env" 2>/dev/null || true
  set +a
fi

echo "WARNING: This will DROP and recreate the velix database."
echo "Backup file: $BACKUP_FILE"
read -p "Continue? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
  echo "Aborted."
  exit 0
fi

echo "[$(date)] Stopping API..."
docker compose --profile full stop api 2>/dev/null || true

echo "[$(date)] Restoring from $BACKUP_FILE..."
if docker inspect velix_postgres &>/dev/null; then
  gunzip -c "$BACKUP_FILE" | docker exec -i velix_postgres psql -U velix -d velix
else
  gunzip -c "$BACKUP_FILE" | psql "${DATABASE_URL}"
fi

echo "[$(date)] Restarting API..."
docker compose --profile full start api 2>/dev/null || true

echo "[$(date)] Restore complete."
