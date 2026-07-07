#!/bin/sh
# Fix ownership of backup volume (mounted as root by Docker)
chown appuser:appuser /backups 2>/dev/null || true
# Drop to non-root user and run the app
exec su-exec appuser finance-tracker "$@"
