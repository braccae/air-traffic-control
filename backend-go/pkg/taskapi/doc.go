// Package taskapi implements the task type system for Air Traffic Control.
//
// It provides:
//   - Canonical task type enumeration ([TaskType]) and built-in task type specifications ([DefaultTaskTypes])
//   - Structured task parameter and request definitions ([TaskParams], [TaskResult], [ValidateTaskParams])
//   - Pluggable task compilation and execution interfaces ([Executor], [FuncExecutor], [PluginActionExecutor])
//   - Thread-safe registry for task type definitions and executors ([Registry])
//   - HTTP API handlers for discovering registered task types (`GET /api/task-types`) and
//     executing or compiling typed tasks (`POST /api/task-types/execute`)
package taskapi
