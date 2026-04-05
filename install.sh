#!/usr/bin/env bash
set -euo pipefail

# UltraBridge CalDAV — Interactive installer
# Safe to re-run: overwrites generated config files each time.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_SUPERNOTE_DIR="/mnt/supernote"
DEFAULT_PORT="8443"
DEFAULT_USERNAME="admin"

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    cat <<EOF
Usage: install.sh [OPTIONS]

Interactive installer for UltraBridge. Prompts for configuration,
builds the Docker image, and starts the service.

Safe to re-run: overwrites generated config files each time.

Options:
  --fresh, -f     Clear the SQLite database before installing
                  (preserves other data in ultrabridge-data/)
  --nuke          Delete ALL UltraBridge data before installing
                  (removes entire ultrabridge-data/ directory)
  -y, --unattended  Non-interactive mode. Reads all configuration from
                    environment variables instead of prompting.
                    Required vars: UB_USERNAME, UB_PASSWORD
                    Optional: UB_PORT, UB_COLLECTION_NAME, UB_NOTES_PATH,
                    UB_SN_SYNC_ENABLED, UB_SN_ACCOUNT, UB_SN_PASSWORD,
                    UB_BOOX_ENABLED, UB_BOOX_NOTES_PATH, etc.
  -h, --help      Show this help message

Prerequisites:
  - Docker and Docker Compose v2
  - Supernote Private Cloud installed and configured
  - MariaDB container running (from the Supernote stack)

Generated files (in your Supernote directory):
  .ultrabridge.env              Environment/config for the container
  docker-compose.override.yml   Compose service definition
  ultrabridge-data/             Persistent data directory
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

# Supernote stack directory
prompt SUPERNOTE_DIR "Supernote Private Cloud directory" "$DEFAULT_SUPERNOTE_DIR"

if [[ ! -f "$SUPERNOTE_DIR/docker-compose.yml" ]]; then
    fail "No docker-compose.yml found in $SUPERNOTE_DIR. Is the Supernote Private Cloud installed?"
fi
ok "Supernote stack found at $SUPERNOTE_DIR"

# .dbenv
if [[ ! -f "$SUPERNOTE_DIR/.dbenv" ]]; then
    fail "No .dbenv found in $SUPERNOTE_DIR. The Supernote Private Cloud must be configured first."
fi
ok ".dbenv found"

# MariaDB running?
if docker ps --format '{{.Names}}' | grep -q mariadb; then
    ok "MariaDB container is running"
else
    warn "MariaDB container doesn't appear to be running."
    if [[ "$UNATTENDED" == true ]]; then
        warn "Continuing in unattended mode..."
    else
        echo "  UltraBridge needs MariaDB. Start the Supernote stack first:"
        echo "  cd $SUPERNOTE_DIR && docker compose up -d"
        echo
        printf 'Continue anyway? [y/N]: '
        read -r yn
        [[ "$yn" =~ ^[Yy] ]] || exit 1
    fi
fi

echo

# --- fresh install ---

DATA_DIR="$SUPERNOTE_DIR/ultrabridge-data"
if [[ "$NUKE_INSTALL" == true ]]; then
    warn "NUKE: deleting ALL UltraBridge data"
    COMPOSE="sudo docker compose -f $SUPERNOTE_DIR/docker-compose.yml -f $SUPERNOTE_DIR/docker-compose.override.yml"
    $COMPOSE stop ultrabridge 2>/dev/null || true
    rm -rf "$DATA_DIR"
    mkdir -p "$DATA_DIR"
    ok "All data deleted"
elif [[ "$FRESH_INSTALL" == true ]]; then
    if [[ -f "$DATA_DIR/ultrabridge.db" ]]; then
        warn "Fresh install: clearing database"
        COMPOSE="sudo docker compose -f $SUPERNOTE_DIR/docker-compose.yml -f $SUPERNOTE_DIR/docker-compose.override.yml"
        $COMPOSE stop ultrabridge 2>/dev/null || true
        rm -f "$DATA_DIR/ultrabridge.db" "$DATA_DIR/ultrabridge.db-wal" "$DATA_DIR/ultrabridge.db-shm"
        ok "Database cleared"
    fi
fi

# --- configuration ---

info "Configuration"
echo
echo "  UltraBridge needs a username and password for CalDAV/web access."
echo "  Your password will be hashed with bcrypt — the plaintext is never stored."
echo

prompt UB_USERNAME "Username" "$DEFAULT_USERNAME"
prompt_password UB_PASSWORD "Password"
prompt UB_PORT "Port to expose on host" "$DEFAULT_PORT"
prompt UB_COLLECTION_NAME "CalDAV collection name" "Supernote Tasks"

echo
info "Notes Pipeline (optional)"
echo
echo "  UltraBridge can scan your .note files, index handwritten text, and"
echo "  optionally run vision-API OCR to extract content from unrecognised pages."
echo
echo "  Before continuing, have these ready:"
echo "    - Full path to your .note files directory"
echo "      (usually /mnt/supernote/note/<your-email@address>)"
echo "    - Full path to a backup directory with sufficient free space"
echo "      (recommended — originals are copied here before any OCR writes)"
echo "    - API credentials if you want OCR"
echo "      (OpenRouter key, or http://localhost:<port> for a local vLLM)"
echo
echo "  Leave the path blank now to skip the pipeline — you can re-run"
echo "  install.sh at any time to enable it later."
echo

prompt UB_NOTES_PATH "Path to your .note files (leave blank to skip)" ""

if [[ -n "$UB_NOTES_PATH" ]]; then
    prompt UB_BACKUP_PATH "Backup directory (copy originals here before OCR writes; leave blank to skip)" ""

    if [[ "$UNATTENDED" == true ]]; then
        yn=$([[ "${UB_OCR_ENABLED:-false}" == "true" ]] && echo "y" || echo "n")
    else
        printf 'Enable OCR via vision API? [y/N]: '
        read -r yn
    fi
    if [[ "$yn" =~ ^[Yy] ]]; then
        UB_OCR_ENABLED=true
        echo
        echo "  API format:"
        echo "    anthropic — Anthropic Messages API (direct Anthropic or OpenRouter)"
        echo "    openai    — OpenAI Chat Completions API (vLLM, Ollama, or compatible)"
        echo
        prompt UB_OCR_FORMAT "API format" "anthropic"
        prompt UB_OCR_API_URL "API base URL" "https://openrouter.ai/api"
        prompt UB_OCR_API_KEY "API key (leave blank for unauthenticated local endpoints)" ""
        prompt UB_OCR_MODEL "Model name" "anthropic/claude-opus-4-6"
    else
        UB_OCR_ENABLED=false
        UB_OCR_FORMAT=""
        UB_OCR_API_URL=""
        UB_OCR_API_KEY=""
        UB_OCR_MODEL=""
    fi
else
    UB_BACKUP_PATH=""
    UB_OCR_ENABLED=false
    UB_OCR_FORMAT=""
    UB_OCR_API_URL=""
    UB_OCR_API_KEY=""
    UB_OCR_MODEL=""
fi

echo
info "── Supernote Task Sync ──"
echo "Sync tasks between UltraBridge and your Supernote device."
echo "This requires the Supernote Private Cloud to be running."
echo ""

UB_SN_SYNC_ENABLED="false"
UB_SN_SYNC_INTERVAL="300"
UB_SN_API_URL=""
UB_SN_PASSWORD=""

if [[ "$UNATTENDED" == true ]]; then
    enable_sync=$([[ "${UB_SN_SYNC_ENABLED:-false}" == "true" ]] && echo "y" || echo "n")
else
    read -rp "Enable Supernote task sync? (y/N): " enable_sync
fi
if [[ "${enable_sync,,}" == "y" ]]; then
    UB_SN_SYNC_ENABLED="true"
    prompt UB_SN_ACCOUNT "Supernote account (email)" ""
    prompt UB_SN_API_URL "SPC API URL" "http://supernote-service:8080"
    prompt UB_SN_SYNC_INTERVAL "Sync interval (seconds)" "300"
    prompt_password UB_SN_PASSWORD "Supernote Private Cloud password"
fi

echo
info "── Boox Device Integration ──"
echo "Boox devices can auto-export handwritten notes via WebDAV."
echo "When enabled, UltraBridge runs a WebDAV server at /webdav/ that"
echo "accepts .note file uploads, renders pages, and indexes text."
echo ""

UB_BOOX_ENABLED="${UB_BOOX_ENABLED:-false}"
UB_BOOX_NOTES_PATH="${UB_BOOX_NOTES_PATH:-}"

if [[ "$UNATTENDED" == true ]]; then
    enable_boox=$([[ "${UB_BOOX_ENABLED:-false}" == "true" ]] && echo "y" || echo "n")
else
    read -rp "Enable Boox device uploads via WebDAV? (y/N): " enable_boox
fi

if [[ "${enable_boox,,}" == "y" ]]; then
    UB_BOOX_ENABLED="true"
    prompt UB_BOOX_NOTES_PATH "Boox notes directory (WebDAV root)" "${SUPERNOTE_DIR}/boox-notes"
    mkdir -p "$UB_BOOX_NOTES_PATH"
fi

echo

# --- build image first (needed for password hashing) ---

info "Building UltraBridge Docker image..."

docker build -t ultrabridge:dev "$SCRIPT_DIR" || fail "Docker build failed"

ok "Image built"

# --- generate bcrypt hash using the binary we just built ---

info "Generating password hash..."

UB_PASSWORD_HASH=$(docker run --rm ultrabridge:dev hash-password "$UB_PASSWORD") \
    || fail "Failed to generate bcrypt hash"

if [[ ! "$UB_PASSWORD_HASH" =~ ^\$2 ]]; then
    fail "Generated hash doesn't look like bcrypt: $UB_PASSWORD_HASH"
fi
ok "Password hashed"

# --- write password hash to secrets file (avoids $$ escaping in compose env_file) ---

info "Writing password hash to secrets file"
printf '%s' "$UB_PASSWORD_HASH" > "$SUPERNOTE_DIR/.ub_password_hash"
chmod 600 "$SUPERNOTE_DIR/.ub_password_hash"
ok "Password hash written (permissions: 600)"

# --- write .ultrabridge.env ---

info "Writing $SUPERNOTE_DIR/.ultrabridge.env"

cat > "$SUPERNOTE_DIR/.ultrabridge.env" <<EOF
# UltraBridge Configuration (generated by install.sh)
# Re-run install.sh to change these settings.
# Password hash is in .ub_password_hash (mounted as /run/secrets/ub_password_hash).

# Auth
UB_USERNAME=$UB_USERNAME

# CalDAV
UB_CALDAV_COLLECTION_NAME=$UB_COLLECTION_NAME
UB_DUE_TIME_MODE=preserve

# Server
UB_LISTEN_ADDR=:8443
UB_WEB_ENABLED=true

# Logging
UB_LOG_LEVEL=info
UB_LOG_FORMAT=json

# Notes pipeline
UB_DB_PATH=/data/ultrabridge.db
$(if [[ -n "$UB_NOTES_PATH" ]]; then echo "UB_NOTES_PATH=$UB_NOTES_PATH"; fi)
$(if [[ -n "$UB_BACKUP_PATH" ]]; then echo "UB_BACKUP_PATH=/backup"; fi)
$(if [[ "$UB_OCR_ENABLED" == "true" ]]; then
echo "UB_OCR_ENABLED=true"
echo "UB_OCR_FORMAT=$UB_OCR_FORMAT"
echo "UB_OCR_API_URL=$UB_OCR_API_URL"
echo "UB_OCR_API_KEY=$UB_OCR_API_KEY"
echo "UB_OCR_MODEL=$UB_OCR_MODEL"
fi)

# Task Store
UB_TASK_DB_PATH=/data/ultrabridge-tasks.db

# Supernote Sync
UB_SN_SYNC_ENABLED=${UB_SN_SYNC_ENABLED}

# Boox Device Integration
UB_BOOX_ENABLED=${UB_BOOX_ENABLED}
EOF

chmod 600 "$SUPERNOTE_DIR/.ultrabridge.env"
ok "Environment file written (permissions: 600)"

# Conditionally append sync-specific vars when enabled
if [[ "$UB_SN_SYNC_ENABLED" == "true" ]]; then
cat >> "$SUPERNOTE_DIR/.ultrabridge.env" <<EOF_SYNC
UB_SN_ACCOUNT=${UB_SN_ACCOUNT}
UB_SN_SYNC_INTERVAL=${UB_SN_SYNC_INTERVAL}
UB_SN_API_URL=${UB_SN_API_URL}
UB_SN_PASSWORD=${UB_SN_PASSWORD}
EOF_SYNC
fi

# Conditionally append Boox path when enabled
if [[ "$UB_BOOX_ENABLED" == "true" ]]; then
    echo "UB_BOOX_NOTES_PATH=${UB_BOOX_NOTES_PATH}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
fi

# --- write docker-compose.override.yml ---

info "Writing $SUPERNOTE_DIR/docker-compose.override.yml"

# Check if an override already exists with non-ultrabridge services
if [[ -f "$SUPERNOTE_DIR/docker-compose.override.yml" ]]; then
    if grep -q 'services:' "$SUPERNOTE_DIR/docker-compose.override.yml" && \
       grep -v -E '^\s*#|^\s*$|ultrabridge|services:|build:|context:|dockerfile:|container_name:|ports:|env_file:|volumes:|depends_on:|restart:' \
       "$SUPERNOTE_DIR/docker-compose.override.yml" | grep -q '[a-z]'; then
        warn "Existing docker-compose.override.yml contains other services."
        echo "  Backing up to docker-compose.override.yml.bak"
        cp "$SUPERNOTE_DIR/docker-compose.override.yml" "$SUPERNOTE_DIR/docker-compose.override.yml.bak"
    fi
fi

# Build volumes list for docker-compose override
VOLUMES_BLOCK=""
# Always mount a data dir for the SQLite DB
mkdir -p "$SUPERNOTE_DIR/ultrabridge-data"
VOLUMES_BLOCK="    volumes:
      - ./ultrabridge-data:/data
      - ./.ub_password_hash:/run/secrets/ub_password_hash:ro"
if [[ -n "$UB_NOTES_PATH" ]]; then
    VOLUMES_BLOCK="$VOLUMES_BLOCK
      - $UB_NOTES_PATH:$UB_NOTES_PATH"
fi
if [[ -n "$UB_BACKUP_PATH" ]]; then
    mkdir -p "$UB_BACKUP_PATH"
    VOLUMES_BLOCK="$VOLUMES_BLOCK
      - $UB_BACKUP_PATH:/backup"
fi
if [[ -n "$UB_BOOX_NOTES_PATH" ]]; then
    VOLUMES_BLOCK="$VOLUMES_BLOCK
      - ${UB_BOOX_NOTES_PATH}:${UB_BOOX_NOTES_PATH}"
fi

cat > "$SUPERNOTE_DIR/docker-compose.override.yml" <<EOF
services:
  ultrabridge:
    image: ultrabridge:dev
    build:
      context: $SCRIPT_DIR
      dockerfile: Dockerfile
    container_name: ultrabridge
    ports:
      - "${UB_PORT}:8443"
    env_file:
      - .ultrabridge.env
      - .dbenv
$VOLUMES_BLOCK
    depends_on:
      - mariadb
    restart: unless-stopped
EOF

ok "Docker Compose override written"

# --- build and start ---

# The Supernote stack's .dbenv is root-owned (600), so docker compose
# needs sudo to read it via env_file. This matches how the Supernote
# stack itself is managed.
COMPOSE="sudo docker compose -f $SUPERNOTE_DIR/docker-compose.yml -f $SUPERNOTE_DIR/docker-compose.override.yml"

echo
info "Starting UltraBridge (sudo required to read .dbenv)..."

$COMPOSE up -d --force-recreate ultrabridge || fail "Failed to start container"

# --- verify ---

info "Verifying..."
sleep 2

HEALTH_URL="http://localhost:${UB_PORT}/health"
if curl -sf "$HEALTH_URL" >/dev/null 2>&1; then
    ok "Health check passed: $HEALTH_URL"
else
    # Give it a few more seconds (DB connection can take a moment)
    sleep 3
    if curl -sf "$HEALTH_URL" >/dev/null 2>&1; then
        ok "Health check passed: $HEALTH_URL"
    else
        warn "Health check failed. Check logs:"
        echo "  $COMPOSE logs ultrabridge"
        echo
        echo "  Common causes:"
        echo "  - MariaDB not running"
        echo "  - No user in u_user table (open Supernote web UI first)"
        exit 1
    fi
fi

# --- done ---

echo
info "UltraBridge is running!"
echo
echo "  Web UI:           http://localhost:${UB_PORT}/"
echo "  Files tab:        http://localhost:${UB_PORT}/files"
echo "  Search tab:       http://localhost:${UB_PORT}/search"
echo "  CalDAV endpoint:  http://localhost:${UB_PORT}/caldav/tasks/"
echo "  CalDAV discovery: http://localhost:${UB_PORT}/.well-known/caldav"
echo "  Health check:     http://localhost:${UB_PORT}/health"
echo
echo "  Username: $UB_USERNAME"
echo "  Password: (the one you just entered)"
echo
echo "  CalDAV client setup:"
echo "    Server URL: https://your-host/.well-known/caldav"
echo "    (Use a reverse proxy like NPM for HTTPS)"
echo
echo "  To reconfigure, just run this script again."
echo "  To view logs: docker logs -f ultrabridge"
echo
