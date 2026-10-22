// Package contextmenu provides the context menu API for Air Traffic Control:
// menu item and section definitions, fluent menu builders, and the menu
// rendering and HTTP delivery interfaces (`GET /api/context-menus`).
//
// It bridges `pkg/plugin/newapi` context menu provider contracts with the
// plugin registry and node resolution layer so both core components and
// registered plugins can contribute structured context menus for nodes,
// containers, VMs, storage pools, shares, networks, firewalls, services, and
// databases.
package contextmenu
