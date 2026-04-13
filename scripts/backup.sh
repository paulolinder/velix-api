#!/usr/bin/env bash
# Velix API — PostgreSQL backup script.
# Run via cron: 0 3 * * * /opt/velix-api/scripts/backup.sh
#
# Configuration via environment or defaults:
#   BACKUP_DIR     — where to store backups (default: /opt/velix-api/backups)
#   BACKUP_RETAIN  — days to keep old backups (default: 30)
#   DATABASE_URL   — PostgreSQL connection string (reads from .env if not set)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Load .env if present.
if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  source "$PROJECT_DIR/.env" 2>/dev/null || true
  set +a
fi

BACKUP_DIR="${BACKUP_DIR:-$PROJECT_DIR/backups}"
BACKUP_RETAIN="${BACKUP_RETAIN:-30}"
DB_URL="${DATABASE_URL:-}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
FILENAME="velix_backup_${TIMESTAMP}.sql.gz"

if [ -z "$DB_URL" ]; then
  echo "ERROR: DATABASE_URL is not set" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"

echo "[$(date)] Starting backup → $BACKUP_DIR/$FILENAME"

# pg_dump via Docker (uses the same Postgres container network).
# Falls back to local pg_dump if Docker container doesn't exist.
if docker inspect velix_postgres &>/dev/null; then
  docker exec velix_postgres pg_dump -U velix velix | gzip > "$BACKUP_DIR/$FILENAME"
else
  pg_dump "$DB_URL" | gzip > "$BACKUP_DIR/$FILENAME"
fi

SIZE=$(du -h "$BACKUP_DIR/$FILENAME" | cut -f1)
echo "[$(date)] Backup complete: $FILENAME ($SIZE)"

# Cleanup old backups.
DELETED=$(find "$BACKUP_DIR" -name "velix_backup_*.sql.gz" -mtime +"$BACKUP_RETAIN" -delete -print | wc -l)
if [ "$DELETED" -gt 0 ]; then
  echo "[$(date)] Removed $DELETED backup(s) older than $BACKUP_RETAIN days"
fi

# Backup WhatsApp sessions (SQLite files).
SESSIONS_DIR="${PROJECT_DIR}/data/instances"
if [ -d "$SESSIONS_DIR" ] && [ "$(ls -A "$SESSIONS_DIR" 2>/dev/null)" ]; then
  SESSIONS_FILE="velix_sessions_${TIMESTAMP}.tar.gz"
  tar czf "$BACKUP_DIR/$SESSIONS_FILE" -C "${PROJECT_DIR}" data/instances/ 2>/dev/null
  SSIZE=$(du -h "$BACKUP_DIR/$SESSIONS_FILE" | cut -f1)
  echo "[$(date)] WhatsApp sessions backup: $SESSIONS_FILE ($SSIZE)"

  # Cleanup old session backups.
  find "$BACKUP_DIR" -name "velix_sessions_*.tar.gz" -mtime +"$BACKUP_RETAIN" -delete 2>/dev/null
fi

echo "[$(date)] Done. Active backups:"
ls -lh "$BACKUP_DIR"/velix_backup_*.sql.gz "$BACKUP_DIR"/velix_sessions_*.tar.gz 2>/dev/null | tail -10
