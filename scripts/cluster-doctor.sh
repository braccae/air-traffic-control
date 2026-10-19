#!/usr/bin/env bash
set -euo pipefail

echo "=================================================="
echo "    Air Traffic Control Cluster Health & Diagnostic   "
echo "=================================================="

# 1. Tailscale Status Check
echo -n "[1/5] Checking Tailscale mesh connection... "
if command -v tailscale > /dev/null 2>&1; then
    TS_STATUS=$(tailscale status --json | jq -r .BackendState 2>/dev/null || echo "Unknown")
    if [ "$TS_STATUS" = "Running" ]; then
        echo -e "\033[32mOK (Running)\033[0m"
    else
        echo -e "\033[33mWARN (State: $TS_STATUS)\033[0m"
    fi
else
    echo -e "\033[31mFAIL (tailscale not installed)\033[0m"
fi

# 2. SSH Agent Check
echo -n "[2/5] Checking SSH Agent & forward sockets... "
if [ -n "${SSH_AUTH_SOCK:-}" ] && [ -S "$SSH_AUTH_SOCK" ]; then
    KEY_COUNT=$(ssh-add -l 2>/dev/null | grep -v "The agent has no identities" | wc -l || echo "0")
    echo -e "\033[32mOK ($KEY_COUNT keys loaded, socket: $SSH_AUTH_SOCK)\033[0m"
else
    FALLBACK_SOCK="/run/user/$(id -u)/ssh-agent.socket"
    if [ -S "$FALLBACK_SOCK" ]; then
        echo -e "\033[33mWARN (Detected fallback at $FALLBACK_SOCK)\033[0m"
    else
        echo -e "\033[31mFAIL (No active SSH_AUTH_SOCK found)\033[0m"
    fi
fi

# 3. Go Daemon Check
echo -n "[3/5] Checking Go daemon (air-traffic-control-daemon :18090)... "
if curl -s http://127.0.0.1:18090/api/health > /dev/null 2>&1; then
    echo -e "\033[32mOK (Responding)\033[0m"
else
    echo -e "\033[33mWARN (Not responding on port 18090)\033[0m"
fi

# 4. SQLite Database Integrity
echo -n "[4/5] Checking SQLite database integrity... "
DB_PATH="data/hosts-cache.sqlite"
if [ -f "$DB_PATH" ]; then
    INTEGRITY=$(sqlite3 "$DB_PATH" "PRAGMA integrity_check;" 2>/dev/null || echo "error")
    if [ "$INTEGRITY" = "ok" ]; then
        NODE_COUNT=$(sqlite3 "$DB_PATH" "SELECT count(*) FROM hosts;" 2>/dev/null || echo "0")
        echo -e "\033[32mOK (Integrity: ok, Hosts cached: $NODE_COUNT)\033[0m"
    else
        echo -e "\033[31mFAIL ($INTEGRITY)\033[0m"
    fi
else
    echo -e "\033[33mWARN (Database $DB_PATH not yet initialized)\033[0m"
fi

# 5. Core Node Pings
echo "[5/5] Checking cluster core nodes latency:"
for host in "likes-it-cold" "ping" "the-freezer"; do
    echo -n "  - $host: "
    if tailscale ping -c 1 "$host" > /tmp/ts_ping.out 2>&1; then
        RTT=$(tailscale ping -c 1 "$host" 2>/dev/null | grep "pong from" | awk '{print $NF}' || echo "<10ms")
        echo -e "\033[32mONLINE ($RTT)\033[0m"
    else
        echo -e "\033[33mUNREACHABLE / DIRECT ONLY\033[0m"
    fi
done
rm -f /tmp/ts_ping.out

echo "=================================================="
echo "Cluster doctor check completed."
