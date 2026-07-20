#!/usr/bin/env bash
# setup-lxc.sh — provision a Waypoint LXC on Proxmox (Ubuntu 22.04/24.04)
#
# Run as root inside the LXC:
#   bash <(curl -fsSL https://raw.githubusercontent.com/gordcurrie/waypoint/main/scripts/setup-lxc.sh)
#
# Or clone first and run locally:
#   git clone https://github.com/gordcurrie/waypoint /opt/waypoint
#   bash /opt/waypoint/scripts/setup-lxc.sh
set -euo pipefail

REPO_URL="https://github.com/gordcurrie/waypoint.git"
INSTALL_DIR="/opt/waypoint"

# ── helpers ────────────────────────────────────────────────────────────────────

log()  { echo "[waypoint] $*"; }
die()  { echo "[waypoint] ERROR: $*" >&2; exit 1; }

require_root() {
  [[ $EUID -eq 0 ]] || die "run as root"
}

# ── Docker ─────────────────────────────────────────────────────────────────────

install_docker() {
  if command -v docker &>/dev/null; then
    log "Docker already installed: $(docker --version)"
    return
  fi

  log "Installing Docker..."
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl git

  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc

  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
    https://download.docker.com/linux/ubuntu \
    $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
    > /etc/apt/sources.list.d/docker.list

  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin

  systemctl enable --now docker
  log "Docker installed: $(docker --version)"
}

# ── repo ───────────────────────────────────────────────────────────────────────

clone_or_update() {
  if [[ -d "$INSTALL_DIR/.git" ]]; then
    log "Updating existing repo at $INSTALL_DIR..."
    git -C "$INSTALL_DIR" pull --ff-only
  else
    log "Cloning waypoint to $INSTALL_DIR..."
    git clone "$REPO_URL" "$INSTALL_DIR"
  fi
}

# ── .env ───────────────────────────────────────────────────────────────────────

setup_env() {
  if [[ -f "$INSTALL_DIR/.env" ]]; then
    log ".env already exists — skipping copy"
    return
  fi
  cp "$INSTALL_DIR/.env.example" "$INSTALL_DIR/.env"
  log "Created $INSTALL_DIR/.env from .env.example"
  log "  → Edit it now: nano $INSTALL_DIR/.env"
}

# ── systemd ────────────────────────────────────────────────────────────────────

install_service() {
  cat > /etc/systemd/system/waypoint.service <<'EOF'
[Unit]
Description=Waypoint fitness tracker (Garmin → InfluxDB → Grafana + MCP)
Documentation=https://github.com/gordcurrie/waypoint
After=docker.service network-online.target
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/waypoint
EnvironmentFile=/opt/waypoint/.env
ExecStart=docker compose -f docker-compose.yml -f docker-compose.homelab.yml up -d --build
ExecStop=docker compose -f docker-compose.yml -f docker-compose.homelab.yml down
TimeoutStartSec=300
Restart=on-failure
RestartSec=30

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable waypoint
  log "systemd service installed and enabled"
}

# ── Garmin auth ────────────────────────────────────────────────────────────────

print_auth_instructions() {
  log ""
  log "── NEXT STEPS ──────────────────────────────────────────────────────────"
  log ""
  log "1. Edit your credentials:"
  log "     nano $INSTALL_DIR/.env"
  log ""
  log "2. Run first-time Garmin auth (interactive MFA required):"
  log "     cd $INSTALL_DIR"
  log "     docker compose -f docker-compose.yml -f docker-compose.homelab.yml build sync"
  log "     docker run --rm -it --env-file .env \\"
  log "       -v waypoint_sync_data:/data \\"
  log "       \$(docker compose -f docker-compose.yml -f docker-compose.homelab.yml images -q sync) \\"
  log "       python auth.py"
  log ""
  log "3. Start the stack:"
  log "     systemctl start waypoint"
  log ""
  log "4. Add Traefik routing — see deploy/traefik-waypoint.yml"
  log "     Update LXC_IP in the file, then copy to your Traefik conf.d/"
  log ""
  log "5. Configure Claude MCP (after Traefik is set up):"
  log '     { "waypoint": { "type": "http", "url": "https://YOUR_MCP_HOST/mcp" } }'
  log ""
}

# ── main ───────────────────────────────────────────────────────────────────────

main() {
  require_root
  install_docker
  clone_or_update
  setup_env
  install_service
  print_auth_instructions
}

main "$@"
