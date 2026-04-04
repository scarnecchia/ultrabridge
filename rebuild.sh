#!/usr/bin/env bash
set -euo pipefail

# UltraBridge CalDAV — Rebuild and restart without reconfiguring.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

info() { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m OK \033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m WARN \033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m FAIL \033[0m %s\n' "$*"; exit 1; }

# Parse arguments: --fresh/-f flag and optional supernote dir
FRESH=false
NUKE=false
SUPERNOTE_DIR="/mnt/supernote"
for arg in "$@"; do
    case "$arg" in
        --fresh|-f) FRESH=true ;;
        --nuke) NUKE=true ;;
        *) SUPERNOTE_DIR="$arg" ;;
    esac
done

if [[ ! -f "$SUPERNOTE_DIR/docker-compose.override.yml" ]]; then
    fail "No docker-compose.override.yml found. Run install.sh first."
fi

COMPOSE="sudo docker compose -f $SUPERNOTE_DIR/docker-compose.yml -f $SUPERNOTE_DIR/docker-compose.override.yml"

# --- fresh install option ---

if [[ "$FRESH" == true ]]; then
    DATA_DIR="$SUPERNOTE_DIR/ultrabridge-data"
    warn "Fresh install requested. This will DELETE:"
    echo "  - Database: $DATA_DIR/ultrabridge.db"
    echo
    printf '  Type "yes" to confirm: '
    read -r confirm
    if [[ "$confirm" != "yes" ]]; then
        fail "Aborted."
    fi
    info "Stopping container..."
    $COMPOSE stop ultrabridge 2>/dev/null || true
    rm -f "$DATA_DIR/ultrabridge.db" "$DATA_DIR/ultrabridge.db-wal" "$DATA_DIR/ultrabridge.db-shm"
    ok "Database cleared"
elif [[ "$NUKE" == true ]]; then
    DATA_DIR="$SUPERNOTE_DIR/ultrabridge-data"
    warn "NUKE requested. This will DELETE EVERYTHING:"
    echo "  - Database: $DATA_DIR/ultrabridge.db"
    echo "  - All data: $DATA_DIR/"
    echo
    printf '  Type "nuke" to confirm: '
    read -r confirm
    if [[ "$confirm" != "nuke" ]]; then
        fail "Aborted."
    fi
    info "Stopping container..."
    $COMPOSE stop ultrabridge 2>/dev/null || true
    rm -rf "$DATA_DIR"
    mkdir -p "$DATA_DIR"
    ok "All data deleted"
fi

info "Building and restarting UltraBridge..."
$COMPOSE up -d --build --force-recreate ultrabridge || fail "Build/restart failed"
ok "Container running"

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
