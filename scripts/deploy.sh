#!/usr/bin/env bash
# Deploy Waypoint to a remote host via SSH.
# Config: copy deploy.env.example → .deploy.env (gitignored) and set values.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "${REPO_ROOT}/.deploy.env" ]]; then
    # shellcheck source=/dev/null
    source "${REPO_ROOT}/.deploy.env"
fi

: "${DEPLOY_HOST:?Set DEPLOY_HOST in .deploy.env (see deploy.env.example)}"
DEPLOY_USER="${DEPLOY_USER:-root}"
DEPLOY_PATH="${DEPLOY_PATH:-/opt/waypoint}"

echo "Deploying to ${DEPLOY_USER}@${DEPLOY_HOST}:${DEPLOY_PATH}"

ssh -o StrictHostKeyChecking=accept-new "${DEPLOY_USER}@${DEPLOY_HOST}" bash -s -- "${DEPLOY_PATH}" <<'REMOTE'
set -euo pipefail
cd "$1"
git pull --ff-only
docker compose -f docker-compose.yml -f docker-compose.homelab.yml up -d --build
echo "Deploy complete."
REMOTE
