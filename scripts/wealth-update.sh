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

# Snapshot local image IDs of configured service tags before pulling so we can
# tell whether `docker compose pull` downloaded any newer image layers.
snapshot_configured_image_ids() {
    local imgs
    imgs="$(docker compose "${COMPOSE_FILES[@]}" config --images | sort -u)"
    if [[ -z "$imgs" ]]; then
        return 0
    fi
    local img
    while IFS= read -r img; do
        [[ -z "$img" ]] && continue
        docker image inspect --format '{{.Id}}' "$img" 2>/dev/null || printf 'missing:%s\n' "$img"
    done <<< "$imgs" | sort -u
}

before="$(snapshot_configured_image_ids)"

log "pulling images"
docker compose "${COMPOSE_FILES[@]}" pull --quiet

after="$(snapshot_configured_image_ids)"

if [[ "$before" == "$after" ]]; then
    log "no image changes"
    exit 0
fi

log "image change detected, recreating containers"
docker compose "${COMPOSE_FILES[@]}" up -d --remove-orphans

log "pruning dangling images"
docker image prune -f >/dev/null

log "done"
