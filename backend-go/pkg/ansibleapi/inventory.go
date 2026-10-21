package ansibleapi

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"air-traffic-control/backend/pkg/hostcache"
)

// CollectionNameRegex validates <namespace>.<collection> format.
var CollectionNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+\.[a-zA-Z0-9_]+$`)

// BuildDynamicInventory generates an Ansible dynamic inventory document
// from hostcache data (hosts, tag chunks, group vars).
func BuildDynamicInventory(store *hostcache.Store) (map[string]any, error) {
	inventory := map[string]any{
		"_meta": map[string]any{
			"hostvars": map[string]any{},
		},
		"all": map[string]any{
			"hosts": []string{},
			"vars":  map[string]any{},
		},
	}

	meta := inventory["_meta"].(map[string]any)
	hostvars := meta["hostvars"].(map[string]any)
	allGroup := inventory["all"].(map[string]any)
	allHosts := allGroup["hosts"].([]string)

	// 1. Hosts
	hosts, err := store.GetAllHosts()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch hosts: %w", err)
	}

	for _, h := range hosts {
		hostID := h.ShortName
		if hostID == "" {
			hostID = h.Name
		}
		if hostID == "" {
			hostID = h.ID
		}

		targetIP := h.TailscaleIP
		if targetIP == "" {
			targetIP = h.IP
		}

		user := h.SSHUser
		if user == "" {
			user = "core"
		}

		allHosts = append(allHosts, hostID)

		hv := map[string]any{
			"ansible_host":     targetIP,
			"ansible_user":     user,
			"ansible_port":     22,
			"tailscale_ip":     h.TailscaleIP,
			"tailscale_online": h.Status == "online",
			"node_id":          h.ID,
		}
		hostvars[hostID] = hv

		// Tag grouping: read tags from NodeJSON if available
		var tags []string
		if len(h.NodeJSON) > 0 {
			var nodeObj struct {
				Tags []string `json:"tags"`
			}
			if err := json.Unmarshal(h.NodeJSON, &nodeObj); err == nil {
				tags = nodeObj.Tags
			}
		}
		for _, tag := range tags {
			cleanTag := strings.ReplaceAll(strings.ReplaceAll(tag, ":", "_"), "-", "_")
			group, exists := inventory[cleanTag]
			if !exists {
				group = map[string]any{"hosts": []string{}}
				inventory[cleanTag] = group
			}
			gMap := group.(map[string]any)
			gHosts := gMap["hosts"].([]string)
			gMap["hosts"] = append(gHosts, hostID)
		}

		// Status groups
		statusGroup := "offline"
		if h.Status == "online" {
			statusGroup = "online"
		}
		if _, exists := inventory[statusGroup]; !exists {
			inventory[statusGroup] = map[string]any{"hosts": []string{}}
		}
		sgMap := inventory[statusGroup].(map[string]any)
		sgMap["hosts"] = append(sgMap["hosts"].([]string), hostID)
	}
	allGroup["hosts"] = allHosts

	// 2. Tag inventory chunks
	chunks, err := store.GetAllTagChunks()
	if err == nil {
		for _, chunk := range chunks {
			cleanTag := strings.ReplaceAll(strings.ReplaceAll(chunk.Tag, ":", "_"), "-", "_")
			if _, exists := inventory[cleanTag]; !exists {
				inventory[cleanTag] = map[string]any{"hosts": []string{}}
			}
			if chunk.SSHUser != "" {
				gMap := inventory[cleanTag].(map[string]any)
				vars, ok := gMap["vars"].(map[string]any)
				if !ok {
					vars = map[string]any{}
					gMap["vars"] = vars
				}
				vars["ansible_user"] = chunk.SSHUser
			}
		}
	}

	// 3. Group vars cache
	groupVars, err := store.GetAllGroupVars()
	if err == nil {
		for filePath, content := range groupVars {
			groupName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
			if groupName == "all" {
				var parsedVars map[string]any
				if err := json.Unmarshal([]byte(content), &parsedVars); err == nil {
					allVars := allGroup["vars"].(map[string]any)
					for k, v := range parsedVars {
						allVars[k] = v
					}
				}
			} else {
				if _, exists := inventory[groupName]; !exists {
					inventory[groupName] = map[string]any{"hosts": []string{}}
				}
			}
		}
	}

	return inventory, nil
}

// InstallGalaxyCollection invokes ansible-galaxy collection install.
func InstallGalaxyCollection(repoRoot string, collection string) (string, error) {
	if !CollectionNameRegex.MatchString(collection) {
		return "", fmt.Errorf("invalid collection name: %s. Format must be <namespace>.<collection>", collection)
	}

	venvDir := GetAnsibleVenvPath(repoRoot)
	galaxyBin := filepath.Join(venvDir, "bin", "ansible-galaxy")
	if _, err := exec.LookPath(galaxyBin); err != nil {
		p, err := exec.LookPath("ansible-galaxy")
		if err != nil {
			return "", fmt.Errorf("ansible-galaxy executable not found")
		}
		galaxyBin = p
	}

	cmd := exec.Command(galaxyBin, "collection", "install", collection)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ansible-galaxy collection install failed (%w): %s", err, string(out))
	}
	return string(out), nil
}
