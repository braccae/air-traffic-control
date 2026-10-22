package newapi

import (
	"air-traffic-control/backend/pkg/plugin"
)

// SettingsScope distinguishes datacenter-wide settings pages from per-node
// settings pages.
type SettingsScope = plugin.SettingsScope

const (
	SettingsScopeDatacenter = plugin.SettingsScopeDatacenter
	SettingsScopeNode       = plugin.SettingsScopeNode
)

// CategorizedSettingsPage extends plugin.SettingsPage with an explicit scope
// ("datacenter" vs "node") and grouping category for sidebar organization.
type CategorizedSettingsPage = plugin.CategorizedSettingsPage

// CategorizedSettingsProvider extends plugin.SettingsPageProvider so plugins
// can declare scoped and categorized settings schemas while remaining 100%
// compatible with existing SettingsPageProvider consumers.
type CategorizedSettingsProvider = plugin.CategorizedSettingsProvider

// ResolveCategorizedSettingsPage returns a normalized CategorizedSettingsPage
// for any SettingsPageProvider. If sp also implements CategorizedSettingsProvider,
// its explicit Scope and Category are preserved (defaulting to
// SettingsScopeDatacenter and "general" when blank).
func ResolveCategorizedSettingsPage(sp plugin.SettingsPageProvider) CategorizedSettingsPage {
	return plugin.ResolveCategorizedSettingsPage(sp)
}

// ValidateCategorizedSettingsPage checks that a CategorizedSettingsPage has a
// valid ID, Scope, and non-empty Category.
func ValidateCategorizedSettingsPage(page CategorizedSettingsPage) error {
	return plugin.ValidateCategorizedSettingsPage(page)
}
