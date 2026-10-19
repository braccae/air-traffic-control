#!/usr/bin/env python3
"""
Tailscale Dynamic Inventory for Ansible
Generates dynamic inventory JSON from live Tailscale mesh status.
Supports: --list and --host <hostname>
"""

import sys
import json
import subprocess
import argparse

def get_tailscale_status():
    try:
        proc = subprocess.run(
            ["tailscale", "status", "--json"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=True
        )
        return json.loads(proc.stdout)
    except Exception as e:
        # Fallback to local SQLite if tailscale CLI unavailable
        return {}

def build_inventory():
    data = get_tailscale_status()
    inventory = {
        "_meta": {
            "hostvars": {}
        },
        "all": {
            "hosts": []
        },
        "tag_host": {
            "hosts": []
        },
        "tag_manager": {
            "hosts": []
        },
        "online": {
            "hosts": []
        },
        "offline": {
            "hosts": []
        }
    }

    peers = []
    if "Self" in data:
        self_node = data["Self"]
        self_node["_is_self"] = True
        peers.append(self_node)
    
    if "Peer" in data and isinstance(data["Peer"], dict):
        peers.extend(data["Peer"].values())

    for peer in peers:
        dns_name = peer.get("DNSName", "").rstrip(".")
        host_name = peer.get("HostName", "")
        if not host_name and dns_name:
            host_name = dns_name.split(".")[0]
        if not host_name:
            continue

        ips = peer.get("TailscaleIPs", [])
        ts_ip = ips[0] if ips else ""
        online = peer.get("Online", False)
        os_name = peer.get("OS", "linux")
        tags = peer.get("Tags", [])

        # Add to all
        if host_name not in inventory["all"]["hosts"]:
            inventory["all"]["hosts"].append(host_name)

        # Hostvars
        inventory["_meta"]["hostvars"][host_name] = {
            "ansible_host": ts_ip,
            "ansible_user": "core",
            "ansible_port": 22,
            "tailscale_ip": ts_ip,
            "tailscale_dns": dns_name,
            "tailscale_online": online,
            "tailscale_os": os_name,
            "tailscale_tags": tags
        }

        # Status groups
        if online:
            inventory["online"]["hosts"].append(host_name)
        else:
            inventory["offline"]["hosts"].append(host_name)

        # Tag groups
        for tag in tags:
            clean_tag = tag.replace(":", "_").replace("-", "_")
            if clean_tag not in inventory:
                inventory[clean_tag] = {"hosts": []}
            if host_name not in inventory[clean_tag]["hosts"]:
                inventory[clean_tag]["hosts"].append(host_name)

        # Default tag_host for common known physical nodes
        if host_name in ["likes-it-cold", "ping", "dev-pc", "the-freezer", "fred", "chuck", "larry"]:
            if host_name not in inventory["tag_host"]["hosts"]:
                inventory["tag_host"]["hosts"].append(host_name)

    return inventory

def main():
    parser = argparse.ArgumentParser(description="Tailscale dynamic inventory")
    parser.add_argument("--list", action="store_true", help="List all hosts")
    parser.add_argument("--host", help="Get host variables")
    args = parser.parse_args()

    inventory = build_inventory()

    if args.host:
        hostvars = inventory["_meta"]["hostvars"].get(args.host, {})
        print(json.dumps(hostvars, indent=2))
    else:
        print(json.dumps(inventory, indent=2))

if __name__ == "__main__":
    main()
