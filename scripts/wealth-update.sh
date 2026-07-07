#!/usr/bin/env bash
# Pull newer images from the registry and recreate containers if anything changed.
# Invoked by wealth-update.timer; safe to run by hand.
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
COMPOSE_FILES=(-f docker-compose.yml)
if [[ "${WEALTH_CADDY:-0}" == "1" ]]; then
    COMPOSE_FILES+=(-f docker-compose.caddy.yml)
fi

cd "$REPO_DIR"

log() { printf '[wealth-update] %s\n' "$*"; }

# Snapshot image IDs before pulling so we can tell whether anything changed.
before="$(docker compose "${COMPOSE_FILES[@]}" images --quiet | sort -u)"

log "pulling images"
docker compose "${COMPOSE_FILES[@]}" pull --quiet

after="$(docker compose "${COMPOSE_FILES[@]}" images --quiet | sort -u)"

if [[ "$before" == "$after" ]]; then
    log "no image changes"
    exit 0
fi

log "image change detected, recreating containers"
docker compose "${COMPOSE_FILES[@]}" up -d --remove-orphans

log "pruning dangling images"
docker image prune -f >/dev/null

log "done"
