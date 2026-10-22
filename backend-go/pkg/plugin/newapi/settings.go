package newapi

import (
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

// SettingsScope distinguishes datacenter-wide settings pages from per-node
// settings pages.
type SettingsScope string

const (
	SettingsScopeDatacenter SettingsScope = "datacenter"
	SettingsScopeNode       SettingsScope = "node"
)

// CategorizedSettingsPage extends plugin.SettingsPage with an explicit scope
// ("datacenter" vs "node") and grouping category for sidebar organization.
type CategorizedSettingsPage struct {
	plugin.SettingsPage
	Scope    SettingsScope `json:"scope"`
	Category string        `json:"category"`
}

// CategorizedSettingsProvider extends plugin.SettingsPageProvider so plugins
// can declare scoped and categorized settings schemas while remaining 100%
// compatible with existing SettingsPageProvider consumers.
type CategorizedSettingsProvider interface {
	plugin.SettingsPageProvider
	CategorizedSettingsPage() CategorizedSettingsPage
}

// ResolveCategorizedSettingsPage returns a normalized CategorizedSettingsPage
// for any SettingsPageProvider. If sp also implements CategorizedSettingsProvider,
// its explicit Scope and Category are preserved (defaulting to
// SettingsScopeDatacenter and "general" when blank).
func ResolveCategorizedSettingsPage(sp plugin.SettingsPageProvider) CategorizedSettingsPage {
	if sp == nil {
		return CategorizedSettingsPage{
			Scope:    SettingsScopeDatacenter,
			Category: "general",
		}
	}
	if csp, ok := sp.(CategorizedSettingsProvider); ok {
		page := csp.CategorizedSettingsPage()
		if page.ID == "" {
			page.SettingsPage = sp.SettingsPage()
		}
		if page.Scope == "" {
			page.Scope = SettingsScopeDatacenter
		}
		if strings.TrimSpace(page.Category) == "" {
			page.Category = defaultCategoryFromPageID(page.ID)
		}
		return page
	}

	base := sp.SettingsPage()
	return CategorizedSettingsPage{
		SettingsPage: base,
		Scope:        SettingsScopeDatacenter,
		Category:     defaultCategoryFromPageID(base.ID),
	}
}

// ValidateCategorizedSettingsPage checks that a CategorizedSettingsPage has a
// valid ID, Scope, and non-empty Category.
func ValidateCategorizedSettingsPage(page CategorizedSettingsPage) error {
	if strings.TrimSpace(page.ID) == "" {
		return fmt.Errorf("settings page ID cannot be empty")
	}
	switch page.Scope {
	case SettingsScopeDatacenter, SettingsScopeNode:
	default:
		return fmt.Errorf("settings page %q has invalid scope %q", page.ID, page.Scope)
	}
	if strings.TrimSpace(page.Category) == "" {
		return fmt.Errorf("settings page %q category cannot be empty", page.ID)
	}
	return nil
}

func defaultCategoryFromPageID(id string) string {
	trimmed := strings.TrimSpace(id)
	if idx := strings.IndexByte(trimmed, '/'); idx > 0 {
		return trimmed[:idx]
	}
	return "general"
}
