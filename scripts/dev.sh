#!/usr/bin/env bash
#
# Single-instance development supervisor for Air Traffic Control.
#
#   scripts/dev.sh                  full stack: Go daemon (go run) + Vite
#   scripts/dev.sh --backend-only   Go daemon only
#   scripts/dev.sh --frontend-only  Vite only
#
# Environment:
#   API_PORT=18090        Go daemon port
#   VITE_PORT=5173        Vite port
#   ATC_DB=<path>         SQLite inventory (defaults to <repo>/data/hosts-cache.sqlite)
#   ATC_RELOAD=0          disable automatic daemon restart on .go changes
#   ATC_CAP_NET_ADMIN=1   build a stable binary and give it CAP_NET_ADMIN
#                         (needs passwordless sudo; used for ATC's own tsnet node
#                         on hosts where a tunnel such as Cloudflare WARP owns the
#                         route to the Tailscale control plane)
#
# The daemon is normally started with `go run .`, so a plain restart is enough to
# pick up backend changes; with ATC_RELOAD (the default) the supervisor watches
# backend-go for .go/go.mod/go.sum changes and restarts it for you.
#
# Every start first reaps the previous dev server. `go run` compiles to a child
# binary, so killing only the `go run` PID leaves that child bound to the API
# port — which is how duplicate daemons and "port already in use" pile up.
#
# ATC_CAP_NET_ADMIN=1 daemons are a special case: `setcap cap_net_admin+ep` makes
# the process non-dumpable, so /proc/<pid>/fd, /proc/<pid>/cwd and /proc/<pid>/exe
# are unreadable even for the owning user. `ss`/`lsof` then cannot attribute the
# listening socket to a PID, which used to leave the old daemon on :18090 forever.
# port_holders() and belongs_to_repo() therefore fall back to command lines.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_PORT="${API_PORT:-18090}"
VITE_PORT="${VITE_PORT:-5173}"
DB_PATH="${ATC_DB:-$ROOT/data/hosts-cache.sqlite}"
RELOAD="${ATC_RELOAD:-1}"
PRIVILEGED="${ATC_CAP_NET_ADMIN:-0}"
DAEMON_BIN="$ROOT/data/bin/air-traffic-control-daemon"

MODE="full"
case "${1:-}" in
  --backend-only) MODE="backend" ;;
  --frontend-only) MODE="frontend" ;;
  "") ;;
  *) echo "!! unknown argument: $1" >&2; exit 2 ;;
esac

log() { printf '>> %s\n' "$*"; }
warn() { printf '!! %s\n' "$*" >&2; }

RUNDIR="$(mktemp -d "${TMPDIR:-/tmp}/atc-dev.XXXXXX")"
DAEMON_PIDFILE="$RUNDIR/daemon.pid"
WATCH_PID=""
VITE_PID=""

# ---------------------------------------------------------------------------
# process helpers
# ---------------------------------------------------------------------------

# port_pids <port> — PIDs listening on a TCP port.
port_pids() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -ltnpH "sport = :${port}" 2>/dev/null | grep -o 'pid=[0-9]\+' | cut -d= -f2 | sort -u
    return 0
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -ti "tcp:${port}" -sTCP:LISTEN 2>/dev/null | sort -u
  fi
}

# kill_tree <pid> — terminate a process and its children, children first, then
# escalate to KILL. `go run` exits on its own once its compiled child is gone.
kill_tree() {
  local pid="$1" kid
  [[ -n "$pid" ]] || return 0
  kill -0 "$pid" 2>/dev/null || return 0
  for kid in $(pgrep -P "$pid" 2>/dev/null || true); do
    kill_tree "$kid"
  done
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 30); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.1
  done
  kill -9 "$pid" 2>/dev/null || true
}

# cmdline_raw <pid> — full command line, empty when unreadable.
cmdline_raw() {
  tr '\0' ' ' <"/proc/${1}/cmdline" 2>/dev/null || true
}

# cmdline_of <pid> — human-readable command line for log lines.
cmdline_of() {
  cmdline_raw "$1" | cut -c1-100
}

# belongs_to_repo <pid> — true when the process runs from this checkout, so the
# reaper never touches an unrelated service (or a different clone).
belongs_to_repo() {
  local pid="$1" cwd exe
  cwd="$(readlink -f "/proc/${pid}/cwd" 2>/dev/null || true)"
  exe="$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)"
  case "$cwd" in "$ROOT" | "$ROOT"/*) return 0 ;; esac
  case "$exe" in "$ROOT"/*) return 0 ;; esac
  # Non-dumpable (capability-carrying) daemons hide cwd/exe; the command line
  # still names this checkout via the binary path or the -db path.
  case "$(cmdline_raw "$pid")" in *"$ROOT/"*) return 0 ;; esac
  return 1
}

# cmdline_port_pids <port> — dev processes whose command line names the port.
# Used when ss/lsof cannot resolve the socket owner (non-dumpable daemon).
cmdline_port_pids() {
  local port="$1" pid
  for pid in $(pgrep -f -- '(air-traffic-control-daemon|prox-orch-daemon|go run \.)' 2>/dev/null || true); do
    if cmdline_raw "$pid" | grep -qE "(^|[[:space:]])-{1,2}port[= ][[:space:]]*${port}([[:space:]]|$)"; then
      printf '%s\n' "$pid"
    fi
  done | sort -u
}

# port_holders <port> — PIDs listening on a TCP port, including the
# command-line fallback for non-dumpable dev daemons.
port_holders() {
  local port="$1" pids
  pids="$(port_pids "$port")"
  [[ -n "$pids" ]] || pids="$(cmdline_port_pids "$port")"
  printf '%s\n' "$pids"
}

# is_dev_process <pid> — a dev server this script may reap (any port), as
# opposed to an unrelated program that merely runs from this checkout.
is_dev_process() {
  local pid="$1" cmd
  [[ "$pid" == "$$" || "$pid" == "$PPID" ]] && return 1
  cmd="$(tr '\0' ' ' <"/proc/${pid}/cmdline" 2>/dev/null || true)"
  [[ "$cmd" =~ ^go[[:space:]]+run[[:space:]]+\.([[:space:]]|$) ]] && return 0
  [[ "$cmd" =~ (air-traffic-control-daemon|prox-orch-daemon)([[:space:]]|$) ]] && return 0
  if [[ "$cmd" == *vite* ]]; then
    [[ "$cmd" == *node* || "$cmd" == npx* || "$cmd" == npm* ]] && return 0
  fi
  return 1
}

# stop_existing — reap every previous dev server for this mode. The port
# holders are authoritative; the sweep catches strays left on a different port
# by an older run, which is how duplicate daemons appear. `--frontend-only`
# never touches the backend, and vice versa.
stop_existing() {
  local pid port holder free=1
  local ports=() patterns=()

  if [[ "$MODE" != "frontend" ]]; then
    ports+=("$API_PORT")
    patterns+=('go run \. ' 'air-traffic-control-daemon|prox-orch-daemon')
  fi
  if [[ "$MODE" != "backend" ]]; then
    ports+=("$VITE_PORT")
    patterns+=('vite')
  fi

  for port in "${ports[@]}"; do
    holder="$(port_holders "$port" | head -1)"
    [[ -n "$holder" ]] || continue
    # Only reap what belongs to this checkout; a system service or another
    # clone on our port is reported, never killed.
    if ! belongs_to_repo "$holder"; then
      warn "port ${port} is held by an unrelated process ${holder}: $(cmdline_of "$holder")"
      warn "stop it yourself or pick another port (API_PORT=… VITE_PORT=… just dev)"
      exit 1
    fi
    log "stopping previous dev server on :${port} (pid ${holder})"
    kill_tree "$holder"
  done

  while read -r pid; do
    [[ -n "$pid" ]] || continue
    belongs_to_repo "$pid" || continue
    is_dev_process "$pid" || continue
    log "stopping stale dev process ${pid}: $(cmdline_of "$pid")"
    kill_tree "$pid"
  done < <(
    {
      for pattern in "${patterns[@]}"; do
        pgrep -f "$pattern" 2>/dev/null || true
      done
    } | grep -E '^[0-9]+$' | sort -u
  )

  # Wait for the kernel to release the listening sockets.
  for _ in $(seq 1 50); do
    free=1
    for port in "${ports[@]}"; do
      [[ -z "$(port_holders "$port")" ]] || free=0
    done
    [[ "$free" == "1" ]] && return 0
    sleep 0.1
  done
  for port in "${ports[@]}"; do
    holder="$(port_holders "$port" | head -1)"
    if [[ -n "$holder" ]]; then
      warn "port ${port} is still held by ${holder}: $(cmdline_of "$holder")"
      exit 1
    fi
  done
}

# ---------------------------------------------------------------------------
# backend
# ---------------------------------------------------------------------------

start_daemon() {
  if [[ "$PRIVILEGED" == "1" ]]; then
    start_privileged_daemon || return 1
  else
    log "Go daemon (go run .) on :${API_PORT} — db ${DB_PATH#"$ROOT"/}"
    (
      cd "$ROOT/backend-go"
      exec go run . -port "$API_PORT" -db "$DB_PATH"
    ) &
  fi
  local pid=$!
  printf '%s\n' "$pid" >"$DAEMON_PIDFILE"
  wait_healthy "$pid"
}

# start_privileged_daemon builds a stable binary and gives it CAP_NET_ADMIN.
# `go run` execs a fresh temp binary on every restart, so file capabilities
# applied there would be lost; a build in data/bin survives supervisor restarts
# and is re-flagged after each rebuild.
start_privileged_daemon() {
  mkdir -p "$(dirname "$DAEMON_BIN")"
  log "building ${DAEMON_BIN#"$ROOT"/} for CAP_NET_ADMIN"
  if ! (cd "$ROOT/backend-go" && go build -o "$DAEMON_BIN" .); then
    warn "privileged daemon build failed"
    return 1
  fi
  if ! apply_cap_net_admin "$DAEMON_BIN"; then
    return 1
  fi
  log "Go daemon (CAP_NET_ADMIN) on :${API_PORT} — db ${DB_PATH#"$ROOT"/}"
  (exec "$DAEMON_BIN" -port "$API_PORT" -db "$DB_PATH") &
}

# apply_cap_net_admin is the user-manager equivalent of
# AmbientCapabilities=CAP_NET_ADMIN in the systemd unit: without it tsnet
# cannot set SO_MARK and falls back to SO_BINDTODEVICE, which pins control-plane
# sockets to one interface and breaks hosts whose policy routing (Cloudflare
# WARP, VPNs) owns the route to the Tailscale control plane.
apply_cap_net_admin() {
  local bin="$1"
  if command -v getcap >/dev/null 2>&1 &&
    getcap "$bin" 2>/dev/null | grep -q cap_net_admin; then
    return 0
  fi
  if ! command -v setcap >/dev/null 2>&1; then
    warn "setcap is missing (install libcap); use a system unit with AmbientCapabilities=CAP_NET_ADMIN instead"
    return 1
  fi
  log "granting CAP_NET_ADMIN to ${bin#"$ROOT"/}"
  if ! sudo -n setcap cap_net_admin+ep "$bin"; then
    warn "could not grant CAP_NET_ADMIN (passwordless sudo required); tsnet will use SO_BINDTODEVICE"
    return 1
  fi
}

wait_healthy() {
  local pid="$1"
  for _ in $(seq 1 400); do
    # Check our own daemon before the HTTP probe: a stale (or foreign) daemon
    # already bound to the port would answer /api/health and mask our failure.
    if ! kill -0 "$pid" 2>/dev/null; then
      local holder
      holder="$(port_holders "$API_PORT" | head -1)"
      if [[ -n "$holder" ]]; then
        warn "Go daemon exited during startup; :${API_PORT} is held by ${holder}: $(cmdline_of "$holder")"
      else
        warn "Go daemon exited during startup"
      fi
      return 1
    fi
    if http_ok; then
      log "daemon healthy on :${API_PORT}"
      return 0
    fi
    sleep 0.25
  done
  warn "Go daemon did not become healthy on :${API_PORT} within 100s"
  return 1
}

# http_ok — /api/health probe, with a plain TCP fallback when curl is absent.
http_ok() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsS "http://127.0.0.1:${API_PORT}/api/health" >/dev/null 2>&1
    return
  fi
  (exec 3<>"/dev/tcp/127.0.0.1/${API_PORT}") 2>/dev/null
}

source_changed() {
  local stamp="$1"
  [[ -n "$(find "$ROOT/backend-go" -name '*.go' -newer "$stamp" -print -quit 2>/dev/null)" ]] && return 0
  [[ "$ROOT/backend-go/go.mod" -nt "$stamp" ]] && return 0
  [[ "$ROOT/backend-go/go.sum" -nt "$stamp" ]] && return 0
  return 1
}

# watch_backend — restart the daemon when backend sources change, and stop the
# session if the daemon dies (e.g. another dev run reaped it). Runs in a
# background subshell; the parent kills it first during cleanup.
watch_backend() {
  local main_pid="$1" stamp="$RUNDIR/stamp" pid
  touch "$stamp"
  while :; do
    sleep 1

    pid="$(cat "$DAEMON_PIDFILE" 2>/dev/null || true)"
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
      warn "daemon is no longer running; stopping dev session"
      kill -TERM "$main_pid" 2>/dev/null || true
      return 1
    fi

    source_changed "$stamp" || continue
    touch "$stamp"
    log "backend change detected — restarting daemon"
    kill_tree "$pid"
    if ! start_daemon; then
      warn "daemon failed to restart; stopping dev session"
      kill -TERM "$main_pid" 2>/dev/null || true
      return 1
    fi
  done
}

# ---------------------------------------------------------------------------
# cleanup
# ---------------------------------------------------------------------------

CLEANED=0
cleanup() {
  [[ "$CLEANED" == "1" ]] && return 0
  CLEANED=1
  local pid=""
  if [[ -n "$WATCH_PID" ]]; then
    kill "$WATCH_PID" 2>/dev/null || true
    wait "$WATCH_PID" 2>/dev/null || true
  fi
  if [[ -n "$VITE_PID" ]]; then
    kill_tree "$VITE_PID" || true
  fi
  pid="$(cat "$DAEMON_PIDFILE" 2>/dev/null || true)"
  if [[ -n "$pid" ]]; then
    kill_tree "$pid" || true
  fi
  rm -rf "$RUNDIR"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

stop_existing

if [[ "$MODE" != "frontend" ]]; then
  start_daemon
  if [[ "$RELOAD" == "1" ]]; then
    watch_backend "$$" &
    WATCH_PID=$!
  fi
fi

if [[ "$MODE" != "backend" ]]; then
  export ATC_API_PORT="$API_PORT"
  host_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  log "Vite on :${VITE_PORT} — open http://${host_ip:-localhost}:${VITE_PORT}"
  npx vite --host 0.0.0.0 --port "$VITE_PORT" --strictPort &
  VITE_PID=$!
  wait "$VITE_PID"
elif [[ -n "$WATCH_PID" ]]; then
  # Backend-only: stay attached to the supervisor so the traps keep working.
  wait "$WATCH_PID" || true
else
  wait "$(cat "$DAEMON_PIDFILE")" || true
fi
