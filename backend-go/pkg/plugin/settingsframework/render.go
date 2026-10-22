package settingsframework

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

// CategorizedPageView pairs a CategorizedSettingsPage schema with its current
// effective (and secret-masked) values for API and UI rendering.
type CategorizedPageView struct {
	newapi.CategorizedSettingsPage
	Values map[string]any `json:"values"`
}

// CategoryGroup organizes multiple CategorizedPageView items under a single
// category for structured sidebar and page rendering.
type CategoryGroup struct {
	Category string                `json:"category"`
	Scope    newapi.SettingsScope  `json:"scope"`
	Pages    []CategorizedPageView `json:"pages"`
}

// ZeroValue returns the typed zero value for a SettingsFieldType.
func ZeroValue(t plugin.SettingsFieldType) any {
	switch t {
	case plugin.SettingsFieldInt:
		return 0
	case plugin.SettingsFieldBool:
		return false
	default:
		return ""
	}
}

// MergeDefaults overlays persisted values on top of a page's schema defaults.
// When maskSecrets is true, any non-empty secret value is replaced with
// plugin.MaskedSecretValue(field.Name) to enforce the Secret Protection
// Invariant on API read paths.
func MergeDefaults(page plugin.SettingsPage, stored map[string]any, maskSecrets bool) map[string]any {
	values := make(map[string]any, len(page.Fields))
	for _, f := range page.Fields {
		if plugin.IsSecretField(f) {
			if v, ok := stored[f.Name]; ok && v != nil {
				if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
					if maskSecrets {
						if plugin.IsMaskedSecretValue(s) {
							values[f.Name] = s
						} else {
							values[f.Name] = plugin.MaskedSecretValue(f.Name)
						}
					} else {
						values[f.Name] = s
					}
					continue
				}
			}
			values[f.Name] = ""
			continue
		}

		if v, ok := stored[f.Name]; ok && v != nil {
			values[f.Name] = v
			continue
		}
		if f.Default != nil {
			values[f.Name] = f.Default
			continue
		}
		values[f.Name] = ZeroValue(f.Type)
	}
	return values
}

// CoerceValues validates an inbound values map against the page schema,
// enforcing field types, required constraints, select options, min/max integer
// bounds, and the Secret Protection Invariant (write-only secrets ignore empty
// or masked values so existing secrets are never overwritten by their mask).
func CoerceValues(page plugin.SettingsPage, input map[string]any) (map[string]any, error) {
	known := make(map[string]plugin.SettingsField, len(page.Fields))
	for _, f := range page.Fields {
		known[f.Name] = f
	}
	for k := range input {
		if _, ok := known[k]; !ok {
			return nil, fmt.Errorf("unknown settings field %q for page %s", k, page.ID)
		}
	}

	out := make(map[string]any, len(page.Fields))
	for _, f := range page.Fields {
		raw, present := input[f.Name]
		if plugin.IsSecretField(f) {
			if !present || raw == nil {
				continue
			}
			s, isString := raw.(string)
			if !isString || strings.TrimSpace(s) == "" || plugin.IsMaskedSecretValue(s) {
				continue
			}
			out[f.Name] = strings.TrimSpace(s)
			continue
		}

		if !present || raw == nil || raw == "" {
			if f.Required {
				return nil, fmt.Errorf("missing required settings field %q", f.Name)
			}
			continue
		}

		coerced, err := CoerceFieldValue(f, raw)
		if err != nil {
			return nil, err
		}
		out[f.Name] = coerced
	}
	return out, nil
}

// CoerceFieldValue validates and converts a single field value according to its
// SettingsField schema (including Min/Max bounds on integers and allowed Enum
// options on select fields).
func CoerceFieldValue(f plugin.SettingsField, raw any) (any, error) {
	switch f.Type {
	case plugin.SettingsFieldInt:
		var parsed int
		switch n := raw.(type) {
		case float64:
			parsed = int(n)
		case int:
			parsed = n
		case int64:
			parsed = int(n)
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				return nil, fmt.Errorf("settings field %q expects an integer", f.Name)
			}
			parsed = int(i)
		case string:
			i, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil {
				return nil, fmt.Errorf("settings field %q expects an integer", f.Name)
			}
			parsed = i
		default:
			return nil, fmt.Errorf("settings field %q expects an integer", f.Name)
		}
		if f.Min != nil && parsed < *f.Min {
			return nil, fmt.Errorf("settings field %q value %d is below minimum %d", f.Name, parsed, *f.Min)
		}
		if f.Max != nil && parsed > *f.Max {
			return nil, fmt.Errorf("settings field %q value %d exceeds maximum %d", f.Name, parsed, *f.Max)
		}
		return parsed, nil

	case plugin.SettingsFieldBool:
		switch b := raw.(type) {
		case bool:
			return b, nil
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(b))
			if err != nil {
				return nil, fmt.Errorf("settings field %q expects a boolean", f.Name)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("settings field %q expects a boolean", f.Name)
		}

	case plugin.SettingsFieldSelect:
		s := fmt.Sprint(raw)
		for _, opt := range f.Options {
			if opt.Value == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("settings field %q value %q is not an allowed option", f.Name, s)

	default: // string, text, secret
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("settings field %q expects a string", f.Name)
		}
		return s, nil
	}
}

// ValidateSchema checks that a SettingsPage has a non-empty ID, non-empty
// Title, unique field names, valid Min/Max ranges, and non-empty options for
// select fields.
func ValidateSchema(page plugin.SettingsPage) error {
	if strings.TrimSpace(page.ID) == "" {
		return fmt.Errorf("settings page ID cannot be empty")
	}
	if strings.TrimSpace(page.Title) == "" {
		return fmt.Errorf("settings page %q title cannot be empty", page.ID)
	}
	seen := make(map[string]struct{}, len(page.Fields))
	for _, f := range page.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			return fmt.Errorf("settings page %q has a field with an empty name", page.ID)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("settings page %q has duplicate field %q", page.ID, name)
		}
		seen[name] = struct{}{}

		if f.Type == plugin.SettingsFieldSelect && len(f.Options) == 0 {
			return fmt.Errorf("settings page %q select field %q must declare at least one option", page.ID, name)
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return fmt.Errorf("settings page %q field %q has min (%d) > max (%d)", page.ID, name, *f.Min, *f.Max)
		}
	}
	return nil
}

// RenderPageView builds a plugin.GlobalSettingsPageView by loading persisted
// values from store, overlaying defaults, and masking secret values.
func RenderPageView(page plugin.SettingsPage, store Store) (plugin.GlobalSettingsPageView, error) {
	stored := map[string]any{}
	if store != nil {
		loaded, err := store.Load(page.ID)
		if err != nil {
			return plugin.GlobalSettingsPageView{}, err
		}
		stored = loaded
	}
	values := MergeDefaults(page, stored, true)
	return plugin.GlobalSettingsPageView{
		SettingsPage: page,
		Values:       values,
	}, nil
}

// RenderCategorizedView builds a CategorizedPageView for a
// newapi.CategorizedSettingsPage (optionally scoped to targetID when Scope is
// "node").
func RenderCategorizedView(page newapi.CategorizedSettingsPage, store Store, targetID string) (CategorizedPageView, error) {
	if page.Scope == "" {
		page.Scope = newapi.SettingsScopeDatacenter
	}
	if strings.TrimSpace(page.Category) == "" {
		page = newapi.ResolveCategorizedSettingsPage(&staticProvider{page: page})
	}
	stored := map[string]any{}
	if store != nil {
		key := ScopedKey(page.Scope, targetID, page.ID)
		loaded, err := store.Load(key)
		if err != nil {
			return CategorizedPageView{}, err
		}
		stored = loaded
	}
	values := MergeDefaults(page.SettingsPage, stored, true)
	return CategorizedPageView{
		CategorizedSettingsPage: page,
		Values:                  values,
	}, nil
}

type staticProvider struct {
	page newapi.CategorizedSettingsPage
}

func (s *staticProvider) SettingsPage() plugin.SettingsPage {
	return s.page.SettingsPage
}

func (s *staticProvider) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return s.page
}

// RenderCategoryGroups organizes a slice of CategorizedSettingsPage definitions
// into sorted CategoryGroup buckets (filtered by scope if scope != ""), each
// populated with its rendered values.
func RenderCategoryGroups(pages []newapi.CategorizedSettingsPage, store Store, scope newapi.SettingsScope, targetID string) ([]CategoryGroup, error) {
	byCategory := make(map[string][]CategorizedPageView)
	var categories []string

	for _, p := range pages {
		resolved := newapi.ResolveCategorizedSettingsPage(&staticProvider{page: p})
		if scope != "" && resolved.Scope != scope {
			continue
		}
		view, err := RenderCategorizedView(resolved, store, targetID)
		if err != nil {
			return nil, err
		}
		if _, exists := byCategory[resolved.Category]; !exists {
			categories = append(categories, resolved.Category)
		}
		byCategory[resolved.Category] = append(byCategory[resolved.Category], view)
	}

	sort.Strings(categories)
	groups := make([]CategoryGroup, 0, len(categories))
	for _, cat := range categories {
		views := byCategory[cat]
		sort.Slice(views, func(i, j int) bool {
			if views[i].Order != views[j].Order {
				return views[i].Order < views[j].Order
			}
			return views[i].ID < views[j].ID
		})
		groupScope := scope
		if groupScope == "" && len(views) > 0 {
			groupScope = views[0].Scope
		}
		groups = append(groups, CategoryGroup{
			Category: cat,
			Scope:    groupScope,
			Pages:    views,
		})
	}
	return groups, nil
}

// Handler serves datacenter and categorized settings HTTP endpoints against a
// plugin.Registry and a settings Store.
type Handler struct {
	registry *plugin.Registry
	store    *StoreAdapter
}

// NewHandler constructs a settings HTTP Handler backed by reg and store.
func NewHandler(reg *plugin.Registry, store Store) *Handler {
	return &Handler{
		registry: reg,
		store:    NewStoreAdapter(store),
	}
}

// RegisterRoutes mounts the categorized settings endpoints onto mux:
//   - GET /api/settings/categories (optional ?scope=datacenter|node&nodeId=...)
//   - GET /api/settings/datacenter (lists all datacenter-scoped page views)
//   - GET, PUT /api/settings/datacenter/:id
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/settings/categories", h.HandleCategories)
	mux.HandleFunc("/api/settings/datacenter", h.HandleDatacenterPages)
	mux.HandleFunc("/api/settings/datacenter/", h.HandleDatacenterPageRoute)
}

func (h *Handler) collectCategorizedPages() []newapi.CategorizedSettingsPage {
	var pages []newapi.CategorizedSettingsPage
	if h.registry == nil {
		return pages
	}
	for _, base := range h.registry.ListSettingsPages() {
		if sp, ok := h.registry.GetSettingsPage(base.ID); ok {
			pages = append(pages, newapi.ResolveCategorizedSettingsPage(sp))
		} else {
			pages = append(pages, newapi.CategorizedSettingsPage{
				SettingsPage: base,
				Scope:        newapi.SettingsScopeDatacenter,
				Category:     "general",
			})
		}
	}
	return pages
}

// HandleCategories implements GET /api/settings/categories.
func (h *Handler) HandleCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	scope := newapi.SettingsScope(strings.TrimSpace(r.URL.Query().Get("scope")))
	nodeID := strings.TrimSpace(r.URL.Query().Get("nodeId"))

	groups, err := RenderCategoryGroups(h.collectCategorizedPages(), h.store.Underlying(), scope, nodeID)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to render settings categories: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(groups)
}

// HandleDatacenterPages implements GET /api/settings/datacenter.
func (h *Handler) HandleDatacenterPages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	all := h.collectCategorizedPages()
	views := make([]CategorizedPageView, 0, len(all))
	for _, page := range all {
		if page.Scope != newapi.SettingsScopeDatacenter {
			continue
		}
		view, err := RenderCategorizedView(page, h.store.Underlying(), "")
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to load settings for %s: %v", page.ID, err), http.StatusInternalServerError)
			return
		}
		views = append(views, view)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(views)
}

// HandleDatacenterPageRoute implements GET and PUT on /api/settings/datacenter/:id.
func (h *Handler) HandleDatacenterPageRoute(w http.ResponseWriter, r *http.Request) {
	pageID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/settings/datacenter/"), "/")
	if pageID == "" || h.registry == nil {
		http.NotFound(w, r)
		return
	}
	sp, ok := h.registry.GetSettingsPage(pageID)
	if !ok {
		http.Error(w, fmt.Sprintf("settings page %q not found", pageID), http.StatusNotFound)
		return
	}
	catPage := newapi.ResolveCategorizedSettingsPage(sp)

	switch r.Method {
	case http.MethodGet:
		view, err := RenderCategorizedView(catPage, h.store.Underlying(), "")
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to load settings for %s: %v", pageID, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)

	case http.MethodPut:
		if h.store.Underlying() == nil {
			http.Error(w, "settings store unavailable", http.StatusServiceUnavailable)
			return
		}
		values, err := decodeValuesPayload(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		if _, err := h.store.ValidateAndSave(catPage.SettingsPage, values); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		view, err := RenderCategorizedView(catPage, h.store.Underlying(), "")
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to reload settings for %s: %v", pageID, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func decodeValuesPayload(body io.Reader) (map[string]any, error) {
	if body == nil {
		return map[string]any{}, nil
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var wrapped plugin.SettingsValuesBody
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Values != nil {
		return wrapped.Values, nil
	}
	var flat map[string]any
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, err
	}
	delete(flat, "values")
	return flat, nil
}
