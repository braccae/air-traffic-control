# Air Traffic Control — dev, test, and build entry points.
#
#   just dev         Express coordinator + Vite middleware on :3000  (current workflow)
#   just dev-split   Go daemon :18090 + Vite :5173 with /api and /ws proxied  (migration workflow)
#
# See docs/superpowers/plans/2026-10-20-node-to-pure-go-migration.md

set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

# ---------------------------------------------------------------------------
# dev
# ---------------------------------------------------------------------------

# Full-stack dev: Express coordinator serves API + WS + Vite HMR on :3000.
dev:
    npm run dev

# Split dev for the migration: Go daemon on :18090, Vite on :5173 proxying /api and /ws.
dev-split:
    #!/usr/bin/env bash
    set -euo pipefail

    API_PORT="${API_PORT:-18090}"
    GO_PID=""

    cleanup() {
      if [[ -n "$GO_PID" ]] && kill -0 "$GO_PID" 2>/dev/null; then
        kill "$GO_PID" 2>/dev/null || true
        wait "$GO_PID" 2>/dev/null || true
      fi
    }
    trap cleanup INT TERM EXIT

    if curl -fsS "http://127.0.0.1:${API_PORT}/api/health" >/dev/null 2>&1; then
      echo "!! something already listening on :${API_PORT} — stop it first" >&2
      exit 1
    fi

    echo ">> Go daemon on :${API_PORT}"
    ( cd backend-go && exec go run . -port "${API_PORT}" ) &
    GO_PID=$!

    for _ in $(seq 1 60); do
      if curl -fsS "http://127.0.0.1:${API_PORT}/api/health" >/dev/null 2>&1; then
        echo ">> daemon healthy"
        break
      fi
      if ! kill -0 "$GO_PID" 2>/dev/null; then
        echo "!! Go daemon exited during startup" >&2
        exit 1
      fi
      sleep 0.25
    done

    echo ">> Vite on :5173 — open http://localhost:5173"
    npx vite

# Go daemon only, on :18090 by default. Override with `just dev-backend 9090`.
dev-backend port="18090":
    cd backend-go && go run . -port {{port}}

# Vite dev server only. Requires the Go daemon on :18090 (see dev-split).
dev-frontend:
    npx vite

# ---------------------------------------------------------------------------
# test and gates
# ---------------------------------------------------------------------------

test:
    npm test -- --run

go-test:
    cd backend-go && go test -v ./...

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
# The `go:embed dist` step lands in Phase 0 of the migration plan.
build-all: build go-build

# ---------------------------------------------------------------------------
# ops
# ---------------------------------------------------------------------------

doctor:
    bash scripts/cluster-doctor.sh

prune-cache:
    npx tsx scripts/prune-cache.ts

clean:
    rm -rf dist /tmp/air-traffic-control-daemon /tmp/prox-orch-daemon
