package ansibleapi

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var safeModuleName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.]*$`)

// IsSafeModuleName validates that a module name does not contain path separators or illegal characters.
func IsSafeModuleName(name string) bool {
	return safeModuleName.MatchString(name) && !strings.Contains(name, "..")
}

// ModuleCatalogEntry represents an item in the module list.
type ModuleCatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ModuleSpecOption mirrors ModuleSpecOption in TypeScript.
type ModuleSpecOption struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Default     any      `json:"default,omitempty"`
	Choices     []string `json:"choices,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
}

// AnsibleModuleSpec mirrors AnsibleModuleSpec in TypeScript.
type AnsibleModuleSpec struct {
	Name             string             `json:"name"`
	RequestedName    string             `json:"requestedName"`
	Collection       string             `json:"collection,omitempty"`
	ShortDescription string             `json:"shortDescription"`
	Description      string             `json:"description"`
	Options          []ModuleSpecOption `json:"options"`
}

var (
	catalogMu    sync.RWMutex
	catalogCache []ModuleCatalogEntry
	catalogExp   time.Time

	specMu    sync.RWMutex
	specCache = make(map[string]AnsibleModuleSpec)
	specExp   = make(map[string]time.Time)
)

// ClearModuleCaches clears in-memory caches.
func ClearModuleCaches() {
	catalogMu.Lock()
	catalogCache = nil
	catalogExp = time.Time{}
	catalogMu.Unlock()

	specMu.Lock()
	specCache = make(map[string]AnsibleModuleSpec)
	specExp = make(map[string]time.Time)
	specMu.Unlock()
}

func getAnsibleDocBinary(repoRoot string) (string, error) {
	if p := os.Getenv("ANSIBLE_DOC_PATH"); p != "" {
		return p, nil
	}
	venvPath := GetAnsibleVenvPath(repoRoot)
	bin := filepath.Join(venvPath, "bin", "ansible-doc")
	if fi, err := os.Stat(bin); err == nil && !fi.IsDir() {
		return bin, nil
	}
	if p, err := exec.LookPath("ansible-doc"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("ansible-doc not found in %s or PATH", bin)
}

// ParseModuleCatalog parses `ansible-doc -t module -l` text output.
func ParseModuleCatalog(stdout string) []ModuleCatalogEntry {
	var entries []ModuleCatalogEntry
	re := regexp.MustCompile(`^(\S+)\s{2,}(.*)$`)
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		matches := re.FindStringSubmatch(trimmed)
		if len(matches) == 3 {
			name := matches[1]
			desc := strings.TrimSpace(matches[2])
			if IsSafeModuleName(name) {
				entries = append(entries, ModuleCatalogEntry{Name: name, Description: desc})
			}
		}
	}
	return entries
}

// ListAnsibleModules returns the catalog of modules.
func ListAnsibleModules(repoRoot string, force bool) ([]ModuleCatalogEntry, error) {
	now := time.Now()
	catalogMu.RLock()
	if !force && catalogCache != nil && catalogExp.After(now) {
		res := catalogCache
		catalogMu.RUnlock()
		return res, nil
	}
	catalogMu.RUnlock()

	docBin, err := getAnsibleDocBinary(repoRoot)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(docBin, "-t", "module", "-l")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ansible-doc failed (%w): %s", err, string(out))
	}

	entries := ParseModuleCatalog(string(out))
	if len(entries) == 0 {
		return nil, fmt.Errorf("ansible-doc returned no modules")
	}

	catalogMu.Lock()
	catalogCache = entries
	catalogExp = now.Add(10 * time.Minute)
	catalogMu.Unlock()

	return entries, nil
}

// NormalizeModuleDoc maps parsed JSON from ansible-doc -j into AnsibleModuleSpec.
func NormalizeModuleDoc(moduleName string, rawJSON []byte) (AnsibleModuleSpec, error) {
	var payload map[string]any
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		return AnsibleModuleSpec{}, fmt.Errorf("invalid json from ansible-doc: %w", err)
	}

	var rootKey string
	var rootVal map[string]any
	for k, v := range payload {
		rootKey = k
		if m, ok := v.(map[string]any); ok {
			rootVal = m
		}
		break
	}

	if rootKey == "" || rootVal == nil {
		return AnsibleModuleSpec{}, fmt.Errorf("ansible-doc returned no documentation for %s", moduleName)
	}

	doc, _ := rootVal["doc"].(map[string]any)
	if doc == nil {
		return AnsibleModuleSpec{}, fmt.Errorf("ansible-doc missing doc property for %s", moduleName)
	}

	shortDesc, _ := doc["short_description"].(string)
	collection, _ := doc["collection"].(string)

	var desc string
	if dStr, ok := doc["description"].(string); ok {
		desc = dStr
	} else if dArr, ok := doc["description"].([]any); ok {
		var lines []string
		for _, item := range dArr {
			lines = append(lines, fmt.Sprintf("%v", item))
		}
		desc = strings.Join(lines, "\n")
	}

	var options []ModuleSpecOption
	if rawOpts, ok := doc["options"].(map[string]any); ok {
		for optName, rawOptVal := range rawOpts {
			optMap, _ := rawOptVal.(map[string]any)
			opt := ModuleSpecOption{
				Name: optName,
				Type: "str",
			}
			if optMap != nil {
				if t, ok := optMap["type"].(string); ok && t != "" {
					opt.Type = t
				}
				if req, ok := optMap["required"].(bool); ok {
					opt.Required = req
				}
				if defVal, exists := optMap["default"]; exists {
					opt.Default = defVal
				}
				if ch, ok := optMap["choices"].([]any); ok {
					for _, c := range ch {
						opt.Choices = append(opt.Choices, fmt.Sprintf("%v", c))
					}
				}
				if al, ok := optMap["aliases"].([]any); ok {
					for _, a := range al {
						opt.Aliases = append(opt.Aliases, fmt.Sprintf("%v", a))
					}
				}
				if d, ok := optMap["description"].(string); ok {
					opt.Description = d
				} else if dArr, ok := optMap["description"].([]any); ok {
					var dLines []string
					for _, item := range dArr {
						dLines = append(dLines, fmt.Sprintf("%v", item))
					}
					opt.Description = strings.Join(dLines, "\n")
				}
			}
			options = append(options, opt)
		}
	}

	return AnsibleModuleSpec{
		Name:             rootKey,
		RequestedName:    moduleName,
		Collection:       collection,
		ShortDescription: shortDesc,
		Description:      desc,
		Options:          options,
	}, nil
}

// GetAnsibleModuleSpec returns the parameter specification for a given module.
func GetAnsibleModuleSpec(repoRoot string, moduleName string, force bool) (AnsibleModuleSpec, error) {
	if !IsSafeModuleName(moduleName) {
		return AnsibleModuleSpec{}, fmt.Errorf("invalid module name: %s", moduleName)
	}

	now := time.Now()
	specMu.RLock()
	if !force {
		if spec, exists := specCache[moduleName]; exists && specExp[moduleName].After(now) {
			specMu.RUnlock()
			return spec, nil
		}
	}
	specMu.RUnlock()

	docBin, err := getAnsibleDocBinary(repoRoot)
	if err != nil {
		return AnsibleModuleSpec{}, err
	}

	cmd := exec.Command(docBin, "-j", moduleName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return AnsibleModuleSpec{}, fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}

	spec, err := NormalizeModuleDoc(moduleName, out)
	if err != nil {
		return AnsibleModuleSpec{}, err
	}

	specMu.Lock()
	specCache[moduleName] = spec
	specExp[moduleName] = now.Add(60 * time.Minute)
	specMu.Unlock()

	return spec, nil
}
