#!/usr/bin/env bash
set -euo pipefail

echo "=================================================="
echo "    PROX-ORCH Pre-Commit Quality & Test Gate       "
echo "=================================================="

echo "[1/4] Checking TypeScript compilation..."
npm run lint

echo "[2/4] Running frontend Vitest unit tests..."
npm test -- --run

echo "[3/4] Running Go backend unit tests..."
(cd backend-go && go test ./...)

echo "[4/4] Validating Ansible playbooks..."
bash scripts/ansible-check.sh

echo "=================================================="
echo -e "\033[32mALL QUALITY CHECKS PASSED SUCCESSFULLY!\033[0m"
echo "=================================================="
