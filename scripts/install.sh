#!/usr/bin/env bash
# Install systemd units that run the wealth docker-compose stack on boot and
# poll the registry for image updates every 5 minutes.
#
# Usage: sudo ./scripts/install.sh [--caddy]
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "must be run as root (use sudo)" >&2
    exit 1
fi

USE_CADDY=0
for arg in "$@"; do
    case "$arg" in
        --caddy) USE_CADDY=1 ;;
        *) echo "unknown argument: $arg" >&2; exit 1 ;;
    esac
done

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
UPDATE_SCRIPT="$REPO_DIR/scripts/wealth-update.sh"
UNIT_DIR="/etc/systemd/system"

chmod +x "$UPDATE_SCRIPT"

compose_args="-f docker-compose.yml"
caddy_env=""
if [[ "$USE_CADDY" == "1" ]]; then
    compose_args+=" -f docker-compose.caddy.yml"
    caddy_env="Environment=WEALTH_CADDY=1"
fi

echo "writing $UNIT_DIR/wealth.service"
cat >"$UNIT_DIR/wealth.service" <<EOF
[Unit]
Description=Wealth docker-compose stack
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=$REPO_DIR
ExecStart=/usr/bin/docker compose $compose_args up -d --remove-orphans
ExecStop=/usr/bin/docker compose $compose_args down
TimeoutStartSec=5min

[Install]
WantedBy=multi-user.target
EOF

echo "writing $UNIT_DIR/wealth-update.service"
cat >"$UNIT_DIR/wealth-update.service" <<EOF
[Unit]
Description=Pull and apply wealth container updates
Requires=docker.service wealth.service
After=docker.service wealth.service

[Service]
Type=oneshot
WorkingDirectory=$REPO_DIR
$caddy_env
ExecStart=$UPDATE_SCRIPT
EOF

echo "writing $UNIT_DIR/wealth-update.timer"
cat >"$UNIT_DIR/wealth-update.timer" <<EOF
[Unit]
Description=Poll for wealth container updates every 5 minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
RandomizedDelaySec=30s
Unit=wealth-update.service
Persistent=true

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now wealth.service
systemctl enable --now wealth-update.timer

echo
echo "installed. useful commands:"
echo "  systemctl status wealth.service"
echo "  systemctl list-timers wealth-update.timer"
echo "  journalctl -u wealth-update.service -n 50"
echo "  systemctl start wealth-update.service   # force an update check"
