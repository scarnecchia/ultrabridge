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
YES=false
SUPERNOTE_DIR="/mnt/supernote"
for arg in "$@"; do
    case "$arg" in
        --fresh|-f) FRESH=true ;;
        --nuke) NUKE=true ;;
        -y|--yes) YES=true ;;
        -h|--help)
            cat <<EOF
Usage: rebuild.sh [OPTIONS] [SUPERNOTE_DIR]

Rebuild and restart UltraBridge without reconfiguring.
Requires install.sh to have been run first.

Options:
  --fresh, -f   Clear both SQLite databases (notes + tasks) before rebuilding
                (prompts for confirmation unless -y)
  --nuke        Delete ALL UltraBridge data before rebuilding
                (prompts for confirmation unless -y)
  -y, --yes     Skip confirmation prompts (for --fresh and --nuke)
  -h, --help    Show this help message

Arguments:
  SUPERNOTE_DIR  Path to Supernote Private Cloud directory
                 (default: /mnt/supernote)
EOF
            exit 0
            ;;
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
    BOOX_PATH=$(grep "^UB_BOOX_NOTES_PATH=" "$SUPERNOTE_DIR/.ultrabridge.env" 2>/dev/null | cut -d= -f2)
    warn "Fresh install requested. This will DELETE:"
    echo "  - Notes database: $DATA_DIR/ultrabridge.db"
    echo "  - Task database:  $DATA_DIR/ultrabridge-tasks.db"
    [[ -n "$BOOX_PATH" ]] && echo "  - Boox cache:     $BOOX_PATH/.cache"
    echo
    if [[ "$YES" != true ]]; then
        printf '  Type "yes" to confirm: '
        read -r confirm
        if [[ "$confirm" != "yes" ]]; then
            fail "Aborted."
        fi
    fi
    info "Stopping container..."
    $COMPOSE stop ultrabridge 2>/dev/null || true
    rm -f "$DATA_DIR/ultrabridge.db" "$DATA_DIR/ultrabridge.db-wal" "$DATA_DIR/ultrabridge.db-shm"
    rm -f "$DATA_DIR/ultrabridge-tasks.db" "$DATA_DIR/ultrabridge-tasks.db-wal" "$DATA_DIR/ultrabridge-tasks.db-shm"
    ok "Databases cleared"

    if [[ -n "$BOOX_PATH" ]]; then
        info "Clearing Boox rendered cache..."
        rm -rf "${BOOX_PATH}/.cache"
        # boox_jobs table will be cleared along with the SQLite database
        # (the DB file is already deleted above)
        # .versions/ is preserved intentionally
        ok "Boox cache cleared"
    fi
elif [[ "$NUKE" == true ]]; then
    DATA_DIR="$SUPERNOTE_DIR/ultrabridge-data"
    BOOX_PATH=$(grep "^UB_BOOX_NOTES_PATH=" "$SUPERNOTE_DIR/.ultrabridge.env" 2>/dev/null | cut -d= -f2)
    warn "NUKE requested. This will DELETE EVERYTHING:"
    echo "  - Notes database: $DATA_DIR/ultrabridge.db"
    echo "  - Task database:  $DATA_DIR/ultrabridge-tasks.db"
    echo "  - All data:       $DATA_DIR/"
    [[ -n "$BOOX_PATH" ]] && echo "  - Boox data:      $BOOX_PATH/.cache + .versions/"
    echo
    if [[ "$YES" != true ]]; then
        printf '  Type "nuke" to confirm: '
        read -r confirm
        if [[ "$confirm" != "nuke" ]]; then
            fail "Aborted."
        fi
    fi
    info "Stopping container..."
    $COMPOSE stop ultrabridge 2>/dev/null || true
    rm -rf "$DATA_DIR"
    mkdir -p "$DATA_DIR"
    ok "All data deleted"

    # Read Boox path from existing config and clear all Boox data
    BOOX_PATH=$(grep "^UB_BOOX_NOTES_PATH=" "$SUPERNOTE_DIR/.ultrabridge.env" 2>/dev/null | cut -d= -f2)
    if [[ -n "$BOOX_PATH" ]]; then
        info "Clearing all Boox data including versions..."
        rm -rf "${BOOX_PATH}/.cache"
        rm -rf "${BOOX_PATH}/.versions"
        ok "Boox data cleared"
    fi
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
