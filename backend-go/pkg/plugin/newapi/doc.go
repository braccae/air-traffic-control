// Package newapi defines the modular v2 plugin API contracts for Air Traffic Control.
//
// It extends the foundational compile-time contracts in package [air-traffic-control/backend/pkg/plugin]
// with structured interfaces and schema definitions for:
//   - Inventory node types ([InventoryNodeType], [NodeTypeSpec], [NodeTypeProvider])
//   - Scoped and categorized settings pages ([SettingsScope], [CategorizedSettingsPage], [CategorizedSettingsProvider])
//   - Context menu builders and providers ([ContextMenuTarget], [ContextMenuEntry], [ContextMenuBuilder], [ContextMenuProvider])
//   - Management plugins ([ManagementActionCategory], [ManagementActionSpec], [ManagementPlugin])
//   - Data collection plugins ([CollectionSchedule], [CollectionSchema], [CollectionEnvelope], [DataCollectionPlugin])
//   - Task type definitions ([TaskType], [TaskTypeSpec], [TaskRequest], [TaskTypeProvider], [TaskCompileProvider])
//
// Package newapi imports [air-traffic-control/backend/pkg/plugin] and never the reverse,
// ensuring a strict, acyclic dependency graph across the foundation packages and all
// registered plugin implementations.
package newapi
