# Air Traffic Control — dev, test, and build entry points.
#
#   just dev         Go daemon :18090 (`go run .`, auto-restart) + Vite :5173
#                    with /api and /ws proxied. Reaps any previous dev server.
#
# See docs/superpowers/plans/2026-10-20-node-to-pure-go-migration.md

set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

# ---------------------------------------------------------------------------
# dev
# ---------------------------------------------------------------------------

# Full-stack dev: `go run .` daemon on :18090 + Vite on :5173; reaps the previous run.
dev: dev-split

# Full-stack dev supervisor — see scripts/dev.sh for ports, reaping and reload.
dev-split:
    ./scripts/dev.sh

# Go daemon only on :18090 (override: `just dev-backend 9090`); reaps and live-reloads.
dev-backend port="18090":
    API_PORT={{port}} ./scripts/dev.sh --backend-only

# Go daemon with CAP_NET_ADMIN (tsnet SO_MARK instead of SO_BINDTODEVICE).
# Needed for ATC's own tsnet node when a tunnel such as Cloudflare WARP owns the
# route to the Tailscale control plane; requires passwordless sudo for setcap.
dev-privileged port="18090":
    ATC_CAP_NET_ADMIN=1 API_PORT={{port}} ./scripts/dev.sh --backend-only

# Vite dev server only; requires the daemon on :18090 and leaves it untouched.
dev-frontend:
    ./scripts/dev.sh --frontend-only

# ---------------------------------------------------------------------------
# test and gates
# ---------------------------------------------------------------------------

test:
    npm test -- --run

go-test:
    cd backend-go && go test -v ./...

# Containerized Playwright e2e. Start `just dev` in another terminal first.
# The Playwright image ships its own browsers, so no browserless/CDP is used.
# Usage: just e2e [playwright args...]   e.g. just e2e tests/e2e/add-node-modal.spec.ts
e2e *args:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -z "${CONTAINER_HOST:-}" || "${CONTAINER_HOST}" == "unix:///run/podman/podman.sock" ]]; then
      if [[ -S "/run/user/$(id -u)/podman/podman.sock" ]]; then
        export CONTAINER_HOST="unix:///run/user/$(id -u)/podman/podman.sock"
      fi
    fi
    engine="${CONTAINER_ENGINE:-podman}"
    image="${PLAYWRIGHT_IMAGE:-mcr.microsoft.com/playwright:v1.64.0-noble}"
    userns=()
    if [[ "$engine" == "podman" ]]; then
      userns=(--userns=keep-id)
    fi
    mkdir -p e2e-results
    "$engine" run --rm --network=host "${userns[@]}" \
      -e HOME=/tmp \
      -e PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
      -e ATC_WEB_URL="${ATC_WEB_URL:-http://127.0.0.1:5173}" \
      -e ATC_API_URL="${ATC_API_URL:-http://127.0.0.1:18090}" \
      -v "$PWD:/work:Z" -w /work \
      "$image" npx playwright test --reporter=line --output=/work/e2e-results {{args}}

lint:
    npm run lint

# All four acceptance gates, in order.
check: lint test go-test build

# ---------------------------------------------------------------------------
# build and run
# ---------------------------------------------------------------------------

go-build:
    cd backend-go && go build -o /tmp/air-traffic-control-daemon . && ln -sf /tmp/air-traffic-control-daemon /tmp/prox-orch-daemon

build:
    npm run build

# Production artifact order: frontend first (emits dist/), then the Go binary.
# The Vite output and the ghostty WASM are copied into backend-go/webui/dist so
# `go:embed` picks them up. The committed .gitkeep anchor is preserved.
build-all: build
    #!/usr/bin/env bash
    set -euo pipefail

    WASM="node_modules/ghostty-web/ghostty-vt.wasm"
    if [[ ! -f "$WASM" ]]; then
      echo "!! $WASM missing — run npm install" >&2
      exit 1
    fi

    find backend-go/webui/dist -mindepth 1 ! -name '.gitkeep' -delete
    cp -R dist/. backend-go/webui/dist/
    cp "$WASM" backend-go/webui/dist/ghostty-vt.wasm

    cd backend-go && go build -o /tmp/air-traffic-control-daemon . && ln -sf /tmp/air-traffic-control-daemon /tmp/prox-orch-daemon
    echo ">> embedded frontend and daemon built"

# ---------------------------------------------------------------------------
# ops
# ---------------------------------------------------------------------------

doctor:
    bash scripts/cluster-doctor.sh

prune-cache:
    npx tsx scripts/prune-cache.ts

clean:
    rm -rf dist /tmp/air-traffic-control-daemon /tmp/prox-orch-daemon
