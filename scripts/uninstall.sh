#!/usr/bin/env bash
# Remove the wealth systemd units. Does not touch containers or volumes.
#
# Usage: sudo ./scripts/uninstall.sh [--down]
#   --down   also run `docker compose down` to stop the stack
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "must be run as root (use sudo)" >&2
    exit 1
fi

STOP_STACK=0
for arg in "$@"; do
    case "$arg" in
        --down) STOP_STACK=1 ;;
        *) echo "unknown argument: $arg" >&2; exit 1 ;;
    esac
done

UNIT_DIR="/etc/systemd/system"

systemctl disable --now wealth-update.timer 2>/dev/null || true
systemctl disable --now wealth-update.service 2>/dev/null || true

if [[ "$STOP_STACK" == "1" ]]; then
    systemctl disable --now wealth.service 2>/dev/null || true
else
    # Leave containers running; just drop the unit's "active" status.
    systemctl disable wealth.service 2>/dev/null || true
fi

rm -f \
    "$UNIT_DIR/wealth.service" \
    "$UNIT_DIR/wealth-update.service" \
    "$UNIT_DIR/wealth-update.timer"

systemctl daemon-reload
systemctl reset-failed wealth.service wealth-update.service wealth-update.timer 2>/dev/null || true

echo "uninstalled."
