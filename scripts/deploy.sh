#!/usr/bin/env bash
# Deploy Waypoint to a remote host via SSH.
# Config: copy deploy.env.example → .deploy.env (gitignored) and set values.
set -euo pipefail

if [[ -f .deploy.env ]]; then
    # shellcheck source=/dev/null
    source .deploy.env
fi

: "${DEPLOY_HOST:?Set DEPLOY_HOST in .deploy.env (see deploy.env.example)}"
DEPLOY_USER="${DEPLOY_USER:-root}"
DEPLOY_PATH="${DEPLOY_PATH:-/opt/waypoint}"

echo "Deploying to ${DEPLOY_USER}@${DEPLOY_HOST}:${DEPLOY_PATH}"

ssh "${DEPLOY_USER}@${DEPLOY_HOST}" bash -s <<REMOTE
set -euo pipefail
cd '${DEPLOY_PATH}'
git pull --ff-only
docker compose -f docker-compose.yml -f docker-compose.homelab.yml build
docker compose -f docker-compose.yml -f docker-compose.homelab.yml up -d
echo "Deploy complete."
REMOTE
