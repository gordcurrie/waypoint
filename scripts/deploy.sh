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
# sync._check_legacy_queue). Refuse to deploy over workouts still waiting in the old file:
# let the running sidecar upload them first (one sync interval), then retry.

# Prints the legacy file's contents, or nothing if it doesn't exist. Fails if the sync
# container can't be reached, so that is never mistaken for "no file".
read_legacy() {
    "${COMPOSE[@]}" exec -T sync sh -c \
        'if [ -e /data/workout_queue.json ]; then cat /data/workout_queue.json; fi'
}
pending() {
    local c="${1//[[:space:]]/}"
    [[ -n "${c}" && "${c}" != "[]" ]]
}

if ! legacy="$(read_legacy)"; then
    if [[ "${SKIP_LEGACY_QUEUE_CHECK}" != "1" ]]; then
        echo "Refusing to deploy: can't read /data/workout_queue.json through the sync" >&2
        echo "container (is it running?). Re-run with SKIP_LEGACY_QUEUE_CHECK=1 to deploy anyway." >&2
        exit 1
    fi
    legacy=""
fi
if [[ -n "${legacy}" ]]; then
    # The file still exists, so the old MCP server is still a possible writer: stop it so
    # nothing can be appended between this check and the rebuild, and re-check. It's
    # restarted on any exit before `up` below recreates it. Once the new sidecar has
    # removed the (empty) file, later deploys skip all of this.
    "${COMPOSE[@]}" stop mcp-server
    trap '"${COMPOSE[@]}" start mcp-server' EXIT
    legacy="$(read_legacy)"
    if pending "${legacy}"; then
        echo "Refusing to deploy: /data/workout_queue.json still holds queued workouts:" >&2
        echo "${legacy}" >&2
        echo "Wait for the next sync run to upload them (or restart the sync container), then retry." >&2
        exit 1
    fi
fi

git pull --ff-only
"${COMPOSE[@]}" up -d --build
trap - EXIT
echo "Deploy complete."
REMOTE
