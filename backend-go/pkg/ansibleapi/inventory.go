package ansibleapi

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/hostcache"
)

// GalaxyCollectionItem describes an installed Ansible Galaxy collection.
type GalaxyCollectionItem struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path,omitempty"`
}

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

func resolveGalaxyBin(repoRoot string) (string, error) {
	if p := ResolveAnsibleBinary(repoRoot, "ansible-galaxy"); p != "" {
		return p, nil
	}
	venvBinDir := filepath.Join(GetAnsibleVenvPath(repoRoot), "bin")
	return "", fmt.Errorf("ansible-galaxy executable not found in %s or PATH", venvBinDir)
}

// ListGalaxyCollections returns installed Ansible Galaxy collections with version and path metadata.
func ListGalaxyCollections(repoRoot string) ([]GalaxyCollectionItem, error) {
	galaxyBin, err := resolveGalaxyBin(repoRoot)
	if err != nil {
		return []GalaxyCollectionItem{}, err
	}

	out, _ := exec.Command(galaxyBin, "collection", "list", "--format", "json").Output()
	byName := make(map[string]GalaxyCollectionItem)

	var rawMap map[string]map[string]struct {
		Version string `json:"version"`
	}
	if len(out) > 0 && json.Unmarshal(out, &rawMap) == nil {
		paths := make([]string, 0, len(rawMap))
		for p := range rawMap {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, basePath := range paths {
			cols := rawMap[basePath]
			for name, meta := range cols {
				if strings.HasPrefix(name, "ansible._") || name == "ansible.builtin" {
					continue
				}
				ver := meta.Version
				if ver == "" || ver == "*" {
					ver = "installed"
				}
				byName[name] = GalaxyCollectionItem{
					Name:    name,
					Version: ver,
					Path:    basePath,
				}
			}
		}
	} else {
		// Fallback to plain text parsing
		txtOut, err := exec.Command(galaxyBin, "collection", "list").CombinedOutput()
		if err != nil && len(txtOut) == 0 {
			return []GalaxyCollectionItem{}, err
		}
		currentPath := ""
		for _, line := range strings.Split(string(txtOut), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "# ") {
				currentPath = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
				continue
			}
			if strings.Contains(trimmed, ".") && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "Collection") && !strings.HasPrefix(trimmed, "---") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 1 && CollectionNameRegex.MatchString(parts[0]) {
					if strings.HasPrefix(parts[0], "ansible._") || parts[0] == "ansible.builtin" {
						continue
					}
					ver := "installed"
					if len(parts) >= 2 {
						ver = parts[1]
					}
					byName[parts[0]] = GalaxyCollectionItem{
						Name:    parts[0],
						Version: ver,
						Path:    currentPath,
					}
				}
			}
		}
	}

	items := make([]GalaxyCollectionItem, 0, len(byName))
	for _, item := range byName {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items, nil
}

// InstallGalaxyCollection invokes ansible-galaxy collection install.
func InstallGalaxyCollection(repoRoot string, collection string) (string, error) {
	if !CollectionNameRegex.MatchString(collection) {
		return "", fmt.Errorf("invalid collection name: %s. Format must be <namespace>.<collection>", collection)
	}

	galaxyBin, err := resolveGalaxyBin(repoRoot)
	if err != nil {
		return "", err
	}

	cmd := exec.Command(galaxyBin, "collection", "install", collection, "--force")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ansible-galaxy collection install failed (%w): %s", err, string(out))
	}
	return string(out), nil
}

// RemoveGalaxyCollection removes an installed Ansible Galaxy collection directory from disk.
func RemoveGalaxyCollection(repoRoot string, collection string) (string, error) {
	collection = strings.TrimSpace(collection)
	if !CollectionNameRegex.MatchString(collection) {
		return "", fmt.Errorf("invalid collection name: %s. Format must be <namespace>.<collection>", collection)
	}
	if collection == "ansible.builtin" {
		return "", fmt.Errorf("cannot remove core collection ansible.builtin")
	}

	parts := strings.SplitN(collection, ".", 2)
	ns, colName := parts[0], parts[1]

	baseDirs := make(map[string]bool)
	if items, err := ListGalaxyCollections(repoRoot); err == nil {
		for _, item := range items {
			if item.Path != "" {
				baseDirs[item.Path] = true
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		baseDirs[filepath.Join(home, ".ansible", "collections", "ansible_collections")] = true
	}
	venvDir := GetAnsibleVenvPath(repoRoot)
	baseDirs[filepath.Join(venvDir, "collections", "ansible_collections")] = true

	var removedFrom []string
	for base := range baseDirs {
		absBase, err := filepath.Abs(base)
		if err != nil {
			continue
		}
		targetDir := filepath.Join(absBase, ns, colName)
		if !strings.HasPrefix(targetDir, absBase+string(filepath.Separator)) {
			continue
		}
		if fi, err := os.Stat(targetDir); err == nil && fi.IsDir() {
			if err := os.RemoveAll(targetDir); err != nil {
				return "", fmt.Errorf("failed to remove %s: %w", targetDir, err)
			}
			removedFrom = append(removedFrom, targetDir)
		}
	}

	if len(removedFrom) == 0 {
		return "", fmt.Errorf("collection %s is not installed", collection)
	}
	return fmt.Sprintf("Removed %s from %s", collection, strings.Join(removedFrom, ", ")), nil
}
