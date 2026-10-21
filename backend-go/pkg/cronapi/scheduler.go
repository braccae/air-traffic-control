package cronapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
)

// Scheduler coordinates the periodic execution of due cron schedules.
type Scheduler struct {
	store      *hostcache.Store
	queueStore *ansqueue.Store
	runner     PlaybookRunner
	repoRoot   string
	broadcast  func(map[string]any)
	stopCh     chan struct{}
	running    bool
	mu         sync.Mutex
	inFlight   map[string]bool
	tickDur    time.Duration
}

// PlaybookRunner interface matching ansibleapi runner if present.
type PlaybookRunner interface {
	RunPlaybook(ctx context.Context, playbook, target string, extraVars map[string]any) (string, error)
}

// SchedulerOptions configures the Scheduler.
type SchedulerOptions struct {
	Store        *hostcache.Store
	QueueStore   *ansqueue.Store
	Runner       PlaybookRunner
	RepoRoot     string
	Broadcast    func(map[string]any)
	TickInterval time.Duration
}

// NewScheduler creates a Scheduler.
func NewScheduler(opts SchedulerOptions) *Scheduler {
	dur := opts.TickInterval
	if dur <= 0 {
		dur = 15 * time.Second
	}
	return &Scheduler{
		store:      opts.Store,
		queueStore: opts.QueueStore,
		runner:     opts.Runner,
		repoRoot:   opts.RepoRoot,
		broadcast:  opts.Broadcast,
		inFlight:   make(map[string]bool),
		tickDur:    dur,
	}
}

// Start launches the background scheduler goroutine.
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	go s.loop()
}

// Stop shuts down the background scheduler ticker.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
}

// IsRunning reports whether the scheduler loop is active.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Scheduler) loop() {
	ticker := time.NewTicker(s.tickDur)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.Tick()
		}
	}
}

// Tick evaluates due schedules and executes them.
func (s *Scheduler) Tick() {
	if s.store == nil {
		return
	}
	nowMs := time.Now().UnixMilli()
	due, err := s.store.ListDueCronSchedules(nowMs)
	if err != nil {
		log.Printf("[cron-scheduler] failed to query due schedules: %v", err)
		return
	}

	for _, cs := range due {
		s.mu.Lock()
		if s.inFlight[cs.ID] {
			s.mu.Unlock()
			continue
		}
		s.inFlight[cs.ID] = true
		s.mu.Unlock()

		// Advance next_run_at BEFORE execution so a crash never re-fires the same slot
		if parsed, err := ParseCronExpression(cs.CronExpression); err == nil {
			next := NextCronRun(parsed, nowMs)
			_ = s.store.SetCronScheduleNextRun(cs.ID, next)
		}

		go func(sched hostcache.CronSchedule) {
			defer func() {
				s.mu.Lock()
				delete(s.inFlight, sched.ID)
				s.mu.Unlock()
			}()

			s.executeSchedule(sched)
		}(cs)
	}
}

// RunNow executes a schedule immediately without advancing its next-run clock.
func (s *Scheduler) RunNow(ctx context.Context, id string) (string, error) {
	if s.store == nil {
		return "", errors.New("Store not configured")
	}
	sched, found, err := s.store.GetCronSchedule(id)
	if err != nil || !found {
		return "", fmt.Errorf("Schedule %s not found", id)
	}
	return s.executeSchedule(sched)
}

func (s *Scheduler) executeSchedule(cs hostcache.CronSchedule) (string, error) {
	startedAt := time.Now().UnixMilli()
	taskID := fmt.Sprintf("cron-task-%d", startedAt)
	status := "OK"
	var runErr error

	_ = s.store.RecordCronScheduleRun(cs.ID, "RUNNING", startedAt, &taskID, nil)

	if s.broadcast != nil {
		if updated, found, _ := s.store.GetCronSchedule(cs.ID); found {
			s.broadcast(map[string]any{"type": "cron_schedules_updated", "schedule": updated})
		}
	}

	// Dispatch logic based on task_kind: playbook or ansible_steps
	switch cs.TaskKind {
	case "playbook":
		// Payload: { playbook: string, limit?: string, checkMode?: bool, tags?: string[], extraVars?: any }
		if s.runner != nil {
			var pld struct {
				Playbook  string         `json:"playbook"`
				Limit     string         `json:"limit"`
				ExtraVars map[string]any `json:"extraVars"`
			}
			_ = json.Unmarshal(cs.PayloadJSON, &pld)
			target := pld.Limit
			if target == "" && cs.ScopeTarget != "" {
				target = cs.ScopeTarget
			}
			_, runErr = s.runner.RunPlaybook(context.Background(), pld.Playbook, target, pld.ExtraVars)
		}

	case "ansible_steps":
		// Payload: { title: string, steps: []Step, category?: string, become?: bool }
		if s.queueStore != nil {
			var pld struct {
				Title    string           `json:"title"`
				Category string           `json:"category"`
				Become   *bool            `json:"become"`
				Steps    []ansqueue.Step  `json:"steps"`
			}
			_ = json.Unmarshal(cs.PayloadJSON, &pld)
			title := pld.Title
			if title == "" {
				title = fmt.Sprintf("Cron: %s", cs.Name)
			}
			cat := pld.Category
			if cat == "" {
				cat = "system"
			}
			targetRoute := cs.ScopeTarget
			if targetRoute == "" {
				targetRoute = "localhost"
			}
			task := ansqueue.Task{
				Title:           title,
				TargetNodeID:    cs.ScopeTarget,
				TargetNodeName:  cs.ScopeTarget,
				TargetHostRoute: targetRoute,
				Category:        cat,
				PrimaryModule:   "ansible.builtin.command",
				Steps:           pld.Steps,
				Become:          pld.Become,
				BecomeMethod:    "run0",
				Status:          ansqueue.StatusStaged,
				CreatedAt:       time.Now().UnixMilli(),
			}
			if len(pld.Steps) > 0 && pld.Steps[0].Module != "" {
				task.PrimaryModule = pld.Steps[0].Module
			}
			_, runErr = s.queueStore.Enqueue(task)
		}
	}

	finishedAt := time.Now().UnixMilli()
	var errStr *string
	if runErr != nil {
		status = "FAILED"
		msg := runErr.Error()
		errStr = &msg
	}

	_ = s.store.RecordCronScheduleRun(cs.ID, status, finishedAt, &taskID, errStr)

	if s.broadcast != nil {
		if updated, found, _ := s.store.GetCronSchedule(cs.ID); found {
			s.broadcast(map[string]any{"type": "cron_schedules_updated", "schedule": updated})
		}
	}

	return taskID, runErr
}
