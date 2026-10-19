#!/usr/bin/env bash
set -euo pipefail

echo "=== PROX-ORCH Ansible Playbook Syntax Checker ==="

INVENTORY="inventory/tailscale_inventory.py"
PLAYBOOK_DIR="ansible/playbooks"

if [ ! -d "$PLAYBOOK_DIR" ]; then
    echo "Playbook directory $PLAYBOOK_DIR does not exist."
    exit 1
fi

FAILURES=0

for pb in "$PLAYBOOK_DIR"/*.yml; do
    echo -n "Checking $pb... "
    if ansible-playbook -i "$INVENTORY" "$pb" --syntax-check > /dev/null 2>&1; then
        echo -e "\033[32mPASS\033[0m"
    else
        echo -e "\033[31mFAIL\033[0m"
        ansible-playbook -i "$INVENTORY" "$pb" --syntax-check || true
        FAILURES=$((FAILURES + 1))
    fi
done

if [ "$FAILURES" -gt 0 ]; then
    echo "Syntax checks failed for $FAILURES playbook(s)."
    exit 1
else
    echo "All playbooks passed syntax check successfully."
    exit 0
fi
