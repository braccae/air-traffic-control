package cronapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
)

// API provides HTTP endpoints for Cron schedules and execution.
type API struct {
	store     *hostcache.Store
	scheduler *Scheduler
	broadcast func(map[string]any)
}

// APIOptions configures the Cron API.
type APIOptions struct {
	Store     *hostcache.Store
	Scheduler *Scheduler
	Broadcast func(map[string]any)
}

// NewAPI creates an instance of Cron API.
func NewAPI(opts APIOptions) (*API, error) {
	return &API{
		store:     opts.Store,
		scheduler: opts.Scheduler,
		broadcast: opts.Broadcast,
	}, nil
}

// Register mounts the cron routes.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/cron/schedules", a.handleSchedules)
	mux.HandleFunc("/api/cron/schedules/", a.handleScheduleSubtree)
	mux.HandleFunc("/api/cron/preview", a.handlePreview)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (a *API) emit(event map[string]any) {
	if a.broadcast != nil {
		a.broadcast(event)
	}
}

func (a *API) handleSchedules(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		schedules, err := a.store.GetAllCronSchedules()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		isRunning := false
		if a.scheduler != nil {
			isRunning = a.scheduler.IsRunning()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":          true,
			"schedules":        schedules,
			"schedulerRunning": isRunning,
		})

	case http.MethodPost:
		var req struct {
			ID             string          `json:"id"`
			Name           string          `json:"name"`
			CronExpression string          `json:"cronExpression"`
			Enabled        *bool           `json:"enabled"`
			Scope          struct {
				Kind   string `json:"kind"`
				Target string `json:"target"`
			} `json:"scope"`
			TaskKind string          `json:"taskKind"`
			Payload  json.RawMessage `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid payload"})
			return
		}

		if strings.TrimSpace(req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Schedule name is required"})
			return
		}
		ok, errStr, canonicalExpr := ValidateCronExpression(req.CronExpression)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Invalid cron expression: %s", errStr)})
			return
		}
		if req.Scope.Kind != "host" && req.Scope.Kind != "group" && req.Scope.Kind != "datacenter" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Invalid scope kind %q", req.Scope.Kind)})
			return
		}
		if req.Scope.Kind != "datacenter" && strings.TrimSpace(req.Scope.Target) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Scope target is required for %s scopes", req.Scope.Kind)})
			return
		}
		if req.TaskKind != "playbook" && req.TaskKind != "ansible_steps" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Invalid task kind %q", req.TaskKind)})
			return
		}

		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		now := time.Now().UnixMilli()
		id := req.ID
		if id == "" {
			rnd := make([]byte, 4)
			_, _ = rand.Read(rnd)
			id = fmt.Sprintf("cron-%d-%s", now, hex.EncodeToString(rnd))
		}

		parsedExpr, _ := ParseCronExpression(canonicalExpr)
		var nextRun *int64
		if enabled {
			nextRun = NextCronRun(parsedExpr, now)
		}

		schedule := hostcache.CronSchedule{
			ID:             id,
			Name:           strings.TrimSpace(req.Name),
			CronExpression: canonicalExpr,
			Enabled:        enabled,
			ScopeKind:      req.Scope.Kind,
			ScopeTarget:    req.Scope.Target,
			TaskKind:       req.TaskKind,
			PayloadJSON:    req.Payload,
			CreatedAt:      now,
			UpdatedAt:      now,
			NextRunAt:      nextRun,
		}

		if err := a.store.UpsertCronSchedule(schedule); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}

		allSchedules, _ := a.store.GetAllCronSchedules()
		a.emit(map[string]any{"type": "cron_schedules_updated", "schedules": allSchedules})
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "schedule": schedule})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

func (a *API) handleScheduleSubtree(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}
	sub := strings.TrimPrefix(r.URL.Path, "/api/cron/schedules/")
	parts := strings.Split(sub, "/")
	id := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			schedule, found, err := a.store.GetCronSchedule(id)
			if err != nil || !found {
				writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("Schedule %s not found", id)})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "schedule": schedule})

		case http.MethodPut:
			existing, found, err := a.store.GetCronSchedule(id)
			if err != nil || !found {
				writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("Schedule %s not found", id)})
				return
			}

			var req struct {
				Name           *string `json:"name"`
				CronExpression *string `json:"cronExpression"`
				Enabled        *bool   `json:"enabled"`
				Scope          *struct {
					Kind   string `json:"kind"`
					Target string `json:"target"`
				} `json:"scope"`
				TaskKind *string          `json:"taskKind"`
				Payload  *json.RawMessage `json:"payload"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid payload"})
				return
			}

			if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
				existing.Name = strings.TrimSpace(*req.Name)
			}
			if req.CronExpression != nil {
				ok, errStr, canonicalExpr := ValidateCronExpression(*req.CronExpression)
				if !ok {
					writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Invalid cron expression: %s", errStr)})
					return
				}
				existing.CronExpression = canonicalExpr
			}
			if req.Enabled != nil {
				existing.Enabled = *req.Enabled
			}
			if req.Scope != nil {
				if req.Scope.Kind != "" {
					existing.ScopeKind = req.Scope.Kind
				}
				existing.ScopeTarget = req.Scope.Target
			}
			if req.TaskKind != nil && *req.TaskKind != "" {
				existing.TaskKind = *req.TaskKind
			}
			if req.Payload != nil {
				existing.PayloadJSON = *req.Payload
			}

			now := time.Now().UnixMilli()
			existing.UpdatedAt = now
			if existing.Enabled {
				parsedExpr, _ := ParseCronExpression(existing.CronExpression)
				existing.NextRunAt = NextCronRun(parsedExpr, now)
			} else {
				existing.NextRunAt = nil
			}

			if err := a.store.UpsertCronSchedule(existing); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}

			allSchedules, _ := a.store.GetAllCronSchedules()
			a.emit(map[string]any{"type": "cron_schedules_updated", "schedules": allSchedules})
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "schedule": existing})

		case http.MethodDelete:
			deleted, err := a.store.DeleteCronSchedule(id)
			if err != nil || !deleted {
				writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("Schedule %s not found", id)})
				return
			}
			allSchedules, _ := a.store.GetAllCronSchedules()
			a.emit(map[string]any{"type": "cron_schedules_updated", "schedules": allSchedules})
			writeJSON(w, http.StatusOK, map[string]any{"success": true})

		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		}
		return
	}

	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		if a.scheduler == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Scheduler not configured"})
			return
		}
		taskID, err := a.scheduler.RunNow(r.Context(), id)
		schedule, _, _ := a.store.GetCronSchedule(id)
		success := err == nil
		writeJSON(w, http.StatusOK, map[string]any{
			"success":  success,
			"run":      map[string]any{"taskId": taskID, "status": schedule.LastRunStatus, "error": err},
			"schedule": schedule,
		})
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Route not found"})
}

func (a *API) handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	expr := r.URL.Query().Get("expression")
	parsed, err := ParseCronExpression(expr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}

	count := 5
	if cStr := r.URL.Query().Get("count"); cStr != "" {
		if c, err := strconv.Atoi(cStr); err == nil && c > 0 {
			count = c
		}
	}
	if count > 25 {
		count = 25
	}

	runs := make([]int64, 0, count)
	cursor := time.Now().UnixMilli()
	for i := 0; i < count; i++ {
		next := NextCronRun(parsed, cursor)
		if next == nil {
			break
		}
		runs = append(runs, *next)
		cursor = *next
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"expression": parsed.Expression,
		"runs":       runs,
	})
}
