package collections

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// galaxyAPIBase is the public Galaxy v3 API. It is a variable so tests can point
// the lookup at a local server instead of the network.
var galaxyAPIBase = "https://galaxy.ansible.com/api/v3/plugin/ansible/content/published/collections/index"

// galaxyHTTPClient bounds every version lookup so an offline or slow Galaxy
// never stalls the settings page.
var galaxyHTTPClient = &http.Client{Timeout: 8 * time.Second}

type galaxyVersionResponse struct {
	Data []struct {
		Version string `json:"version"`
	} `json:"data"`
}

// fetchLatest resolves the newest published version for each collection. A
// failure for one collection never fails the batch: the caller reports it per
// entry so the UI can say "unknown" instead of inventing a version.
func (m *Manager) fetchLatest(names []string) (map[string]LatestVersion, map[string]error) {
	results := make(map[string]LatestVersion, len(names))
	errs := make(map[string]error, len(names))

	for _, name := range names {
		namespace, collection, ok := splitCollectionName(name)
		if !ok {
			errs[name] = fmt.Errorf("invalid collection name %q", name)
			continue
		}
		url := fmt.Sprintf("%s/%s/%s/versions/?limit=1&ordering=-version", galaxyAPIBase, namespace, collection)

		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			errs[name] = err
			continue
		}
		req.Header.Set("Accept", "application/json")

		resp, err := galaxyHTTPClient.Do(req)
		if err != nil {
			cancel()
			errs[name] = fmt.Errorf("galaxy lookup failed: %w", err)
			continue
		}
		var payload galaxyVersionResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		cancel()

		if resp.StatusCode != http.StatusOK {
			errs[name] = fmt.Errorf("galaxy lookup returned HTTP %d", resp.StatusCode)
			continue
		}
		if decodeErr != nil {
			errs[name] = fmt.Errorf("galaxy lookup returned invalid JSON: %w", decodeErr)
			continue
		}
		if len(payload.Data) == 0 || strings.TrimSpace(payload.Data[0].Version) == "" {
			errs[name] = fmt.Errorf("galaxy has no published versions")
			continue
		}

		results[name] = LatestVersion{
			Version:   payload.Data[0].Version,
			CheckedAt: time.Now().UnixMilli(),
		}
	}

	return results, errs
}

// splitCollectionName splits "namespace.name" into its parts.
func splitCollectionName(name string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(name), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
