#!/usr/bin/env bash
set -euo pipefail

# UltraBridge Installer
# Minimal configuration: port, username, password.
# All other configuration via Settings UI on first boot.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_PORT="8443"
DEFAULT_USERNAME="admin"

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    cat <<EOF
Usage: install.sh [OPTIONS]

Minimal installer for UltraBridge. Prompts for port, username, and password,
then starts the container. All other configuration is done via the Settings UI
on first boot.

Safe to re-run: overwrites generated config files each time.

Options:
  --fresh, -f     Clear the SQLite database before installing
                  (preserves other data in ultrabridge-data/)
  --nuke          Delete ALL UltraBridge data before installing
                  (removes entire ultrabridge-data/ directory)
  -y, --unattended  Non-interactive mode. Reads port, username, password from
                    environment variables: UB_PORT, UB_USERNAME, UB_PASSWORD
  -h, --help      Show this help message

Prerequisites:
  - Docker and Docker Compose v2

Generated files (in current directory):
  docker-compose.yml      Compose service definition
  ultrabridge-data/       Persistent data directory
EOF
    exit 0
fi

# --- helpers ---

info()  { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()    { printf '\033[1;32m OK \033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m WARN \033[0m %s\n' "$*"; }
fail()  { printf '\033[1;31m FAIL \033[0m %s\n' "$*"; exit 1; }

prompt() {
    local var="$1" msg="$2" default="$3"
    if [[ "$UNATTENDED" == true ]]; then
        # In unattended mode, use existing env var value or default
        local current="${!var:-$default}"
        eval "$var=\"$current\""
        return
    fi
    local input
    if [[ -n "$default" ]]; then
        printf '%s [%s]: ' "$msg" "$default"
    else
        printf '%s: ' "$msg"
    fi
    read -r input
    eval "$var=\"${input:-$default}\""
}

prompt_password() {
    local var="$1" msg="$2"
    if [[ "$UNATTENDED" == true ]]; then
        # In unattended mode, use existing env var value
        if [[ -z "${!var:-}" ]]; then
            fail "Required variable $var is not set (unattended mode)"
        fi
        return
    fi
    local pw1 pw2
    while true; do
        printf '%s: ' "$msg"
        read -rs pw1
        echo
        printf 'Confirm password: '
        read -rs pw2
        echo
        if [[ "$pw1" == "$pw2" ]]; then
            if [[ -z "$pw1" ]]; then
                warn "Password cannot be empty. Try again."
                continue
            fi
            eval "$var=\"$pw1\""
            return
        fi
        warn "Passwords don't match. Try again."
    done
}

# --- parse arguments ---

FRESH_INSTALL=false
NUKE_INSTALL=false
UNATTENDED=false
for arg in "$@"; do
    case "$arg" in
        --fresh|-f) FRESH_INSTALL=true ;;
        --nuke) NUKE_INSTALL=true ;;
        -y|--unattended) UNATTENDED=true ;;
    esac
done

# --- pre-flight checks ---

info "UltraBridge Installer"
echo

# Docker
if ! command -v docker &>/dev/null; then
    fail "Docker is not installed. Install Docker first."
fi
ok "Docker found"

# Docker Compose
if ! docker compose version &>/dev/null; then
    fail "Docker Compose (v2) not found. Install docker-compose-plugin."
fi
ok "Docker Compose found"

echo

# --- fresh install ---

DATA_DIR="./ultrabridge-data"
if [[ "$NUKE_INSTALL" == true ]] || [[ "$FRESH_INSTALL" == true ]]; then
    # Stop container if running
    docker stop ultrabridge 2>/dev/null || true
    if [[ "$NUKE_INSTALL" == true ]]; then
        warn "NUKE: deleting ALL UltraBridge data"
        rm -rf "$DATA_DIR"
        mkdir -p "$DATA_DIR"
        ok "All data deleted"
    elif [[ -f "$DATA_DIR/ultrabridge.db" ]]; then
        warn "Fresh install: clearing database"
        rm -f "$DATA_DIR/ultrabridge.db" "$DATA_DIR/ultrabridge.db-wal" "$DATA_DIR/ultrabridge.db-shm"
        rm -f "$DATA_DIR/ultrabridge-tasks.db" "$DATA_DIR/ultrabridge-tasks.db-wal" "$DATA_DIR/ultrabridge-tasks.db-shm"
        ok "Database cleared"
    fi
fi

# --- configuration ---

info "Configuration"
echo
echo "  Bootstrap configuration: port, username, password"
echo "  All other settings will be configured via the Settings UI on first boot."
echo

prompt UB_USERNAME "Username" "${UB_USERNAME:-$DEFAULT_USERNAME}"
prompt_password UB_PASSWORD "Password"
prompt UB_PORT "Port to expose on host" "${UB_PORT:-$DEFAULT_PORT}"

echo

# --- build image ---

info "Building UltraBridge Docker image..."

docker build -t ultrabridge:latest "$SCRIPT_DIR" || fail "Docker build failed"

ok "Image built"

# --- generate docker-compose.yml ---

info "Generating docker-compose.yml"

mkdir -p "$DATA_DIR"

# Detect if Supernote Private Cloud is running and join its network if it is.
SN_NETWORK=""
if docker ps --format '{{.Names}}' | grep -q "^supernote-service$"; then
    SN_NETWORK=$(docker inspect supernote-service --format '{{range $net, $conf := .NetworkSettings.Networks}}{{$net}}{{end}}' 2>/dev/null || true)
    if [[ -n "$SN_NETWORK" ]]; then
        ok "Detected Supernote Private Cloud network: $SN_NETWORK"
    fi
fi

# Detect if Supernote directory exists and mount it if it does.
VOLUMES_BLOCK="      - ./ultrabridge-data:/data"
if [[ -d "/mnt/supernote" ]]; then
    ok "Detected Supernote directory at /mnt/supernote"
    VOLUMES_BLOCK="$VOLUMES_BLOCK
      - /mnt/supernote:/mnt/supernote"
fi

NETWORKS_BLOCK=""
JOIN_NETWORKS=""
if [[ -n "$SN_NETWORK" ]]; then
    JOIN_NETWORKS="
    networks:
      - default
      - $SN_NETWORK"
    NETWORKS_BLOCK="
networks:
  default:
    name: ultrabridge_default
  $SN_NETWORK:
    external: true"
fi

cat > "$SCRIPT_DIR/docker-compose.yml" <<EOF
services:
  ultrabridge:
    image: ultrabridge:latest
    build:
      context: .
      dockerfile: Dockerfile
    container_name: ultrabridge
    ports:
      - "${UB_PORT}:8443"
    environment:
      - UB_DB_PATH=/data/ultrabridge.db
      - UB_LISTEN_ADDR=:8443
      - UB_TASK_DB_PATH=/data/ultrabridge-tasks.db
    volumes:
$VOLUMES_BLOCK$JOIN_NETWORKS
    restart: unless-stopped$NETWORKS_BLOCK
EOF

ok "Docker Compose file written"

echo

# --- start ---

info "Starting UltraBridge..."

# Remove any existing container with this name (from prior install or manual docker run)
docker rm -f ultrabridge 2>/dev/null || true

docker compose -f "$SCRIPT_DIR/docker-compose.yml" up -d --force-recreate ultrabridge || fail "Failed to start container"

# --- verify ---

HEALTH_URL="http://localhost:${UB_PORT}/health"
HEALTH_TIMEOUT=180
info "Waiting for health check (up to ${HEALTH_TIMEOUT}s)..."
HEALTH_OK=false
for i in $(seq 1 $HEALTH_TIMEOUT); do
    if curl -sf "$HEALTH_URL" >/dev/null 2>&1; then
        HEALTH_OK=true
        break
    fi
    # Show progress every 10 seconds
    if (( i % 10 == 0 )); then
        echo "  ... still waiting (${i}s)"
    fi
    sleep 1
done

if [[ "$HEALTH_OK" == true ]]; then
    ok "Health check passed (${i}s)"
else
    warn "Health check failed after ${HEALTH_TIMEOUT}s. Check logs:"
    echo "  docker compose logs ultrabridge"
    echo
    echo "  Common causes:"
    echo "  - Port $UB_PORT already in use"
    echo "  - Slow startup (database migration)"
    exit 1
fi

# --- seed user credentials ---

info "Seeding initial user credentials..."

docker exec ultrabridge ultrabridge seed-user "$UB_USERNAME" "$UB_PASSWORD" || fail "Failed to seed user credentials"

ok "User credentials saved"

# --- done ---

echo
info "UltraBridge is running!"
echo
echo "  Web UI:           http://localhost:${UB_PORT}/"
echo "  Health check:     http://localhost:${UB_PORT}/health"
echo
echo "  Username: $UB_USERNAME"
echo "  Password: (the one you just entered)"
echo
echo "  Complete configuration in the Settings UI."
echo
echo "  To reconfigure, just run this script again."
echo "  To view logs: docker logs -f ultrabridge"
echo
