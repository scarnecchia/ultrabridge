#!/usr/bin/env bash
set -euo pipefail

# UltraBridge CalDAV — Rebuild and restart without reconfiguring.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUPERNOTE_DIR="${1:-/mnt/supernote}"

info() { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m OK \033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m FAIL \033[0m %s\n' "$*"; exit 1; }

if [[ ! -f "$SUPERNOTE_DIR/docker-compose.override.yml" ]]; then
    fail "No docker-compose.override.yml found. Run install.sh first."
fi

info "Building UltraBridge..."
docker build -t ultrabridge:dev "$SCRIPT_DIR" || fail "Build failed"
ok "Image built"

info "Restarting container..."
sudo docker compose -f "$SUPERNOTE_DIR/docker-compose.yml" \
                    -f "$SUPERNOTE_DIR/docker-compose.override.yml" \
                    up -d ultrabridge || fail "Restart failed"

sleep 2
PORT=$(grep -oP '"\K\d+(?=:8443")' "$SUPERNOTE_DIR/docker-compose.override.yml" || echo "8443")
if curl -sf "http://localhost:${PORT}/health" >/dev/null 2>&1; then
    ok "Health check passed"
else
    sleep 3
    if curl -sf "http://localhost:${PORT}/health" >/dev/null 2>&1; then
        ok "Health check passed"
    else
        fail "Health check failed. Run: sudo docker logs ultrabridge"
    fi
fi

info "Done!"
