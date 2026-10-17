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
COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.homelab.yml)

# The sync sidecar no longer reads the old single-file workout queue (it moved to the
# workout_queue/ spool dir, and deliberately doesn't auto-migrate — see
# sync._check_legacy_queue). Refuse to deploy over workouts still waiting in the old file:
# let the running sidecar upload them first (one sync interval), then retry.
legacy="$("${COMPOSE[@]}" exec -T sync cat /data/workout_queue.json 2>/dev/null || true)"
if [[ -n "${legacy//[[:space:]]/}" && "${legacy//[[:space:]]/}" != "[]" ]]; then
    echo "Refusing to deploy: /data/workout_queue.json still holds queued workouts:" >&2
    echo "${legacy}" >&2
    echo "Wait for the next sync run to upload them (or restart the sync container), then retry." >&2
    exit 1
fi

git pull --ff-only
"${COMPOSE[@]}" up -d --build
echo "Deploy complete."
REMOTE
