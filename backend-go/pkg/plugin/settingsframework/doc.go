// Package settingsframework provides a declarative builder, store abstraction,
// validation engine, and rendering/persistence helpers for datacenter-wide and
// node-scoped settings pages in Air Traffic Control.
//
// It wraps and extends plugin.SettingsPage, plugin.SettingsStore, and
// newapi.CategorizedSettingsPage so addon modules can declare strongly-typed
// settings schemas with minimal boilerplate while upholding the Secret
// Protection Invariant ("•••••••• (vault: <name>)").
package settingsframework
