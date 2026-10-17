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

ssh -o StrictHostKeyChecking=accept-new "${DEPLOY_USER}@${DEPLOY_HOST}" \
    bash -s -- "${DEPLOY_PATH}" "${SKIP_LEGACY_QUEUE_CHECK:-}" <<'REMOTE'
set -euo pipefail
cd "$1"
SKIP_LEGACY_QUEUE_CHECK="$2"
COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.homelab.yml)

# The sync sidecar no longer reads the old single-file workout queue (it moved to the
# workout_queue/ spool dir, and deliberately doesn't auto-migrate — see
# sync._check_legacy_queue). Deploying over a pre-spool version must not strand workouts
# still waiting in the old file: let the running sidecar upload them first, then retry.
#
# Prints "spool" if the running deployment is already spool-aware (the sidecar creates
# /data/workout_queue/ every run), else the legacy file's contents (nothing if absent).
# Fails if the sync container can't be reached, so that's never mistaken for "no file".
queue_state() {
    "${COMPOSE[@]}" exec -T sync sh -c '
        if [ -d /data/workout_queue ]; then echo spool
        elif [ -e /data/workout_queue.json ]; then cat /data/workout_queue.json
        fi'
}
pending() {
    local c="${1//[[:space:]]/}"
    [[ -n "${c}" && "${c}" != "[]" && "${c}" != "spool" ]]
}

if ! state="$(queue_state)"; then
    if [[ "${SKIP_LEGACY_QUEUE_CHECK}" != "1" ]]; then
        echo "Refusing to deploy: can't check the workout queue through the sync container" >&2
        echo "(is it running?). Re-run with SKIP_LEGACY_QUEUE_CHECK=1 to deploy anyway." >&2
        exit 1
    fi
    state="spool"
fi
if [[ "${state}" != "spool" ]]; then
    # Pre-spool deployment: its MCP server writes the legacy file — even one that doesn't
    # exist yet — so stop it, so nothing can be queued there between this check and the
    # rebuild, then re-check. Restarted on any exit before `up` below recreates it. Once
    # the new sidecar has run, later deploys skip all of this.
    "${COMPOSE[@]}" stop mcp-server
    trap '"${COMPOSE[@]}" start mcp-server' EXIT
    state="$(queue_state)"
    if pending "${state}"; then
        echo "Refusing to deploy: /data/workout_queue.json still holds queued workouts:" >&2
        echo "${state}" >&2
        echo "Wait for the next sync run to upload them (or restart the sync container), then retry." >&2
        exit 1
    fi
fi

git pull --ff-only
"${COMPOSE[@]}" up -d --build
trap - EXIT
echo "Deploy complete."
REMOTE
