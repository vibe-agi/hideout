package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
)

const (
	EnvironmentActionStop  = "stop"
	EnvironmentActionClean = "clean"
)

type EnvironmentActionOptions struct {
	IDs         []string
	StoppedOnly bool
	Idle        time.Duration
	IdleSet     bool
	Now         time.Time
}

type EnvironmentActionFilter struct {
	StoppedOnly bool   `json:"stoppedOnly,omitempty"`
	Idle        string `json:"idle,omitempty"`
}

type EnvironmentActionPlan struct {
	Action       string                    `json:"action"`
	RequestedIDs []string                  `json:"requestedIds,omitempty"`
	Filter       EnvironmentActionFilter   `json:"filter,omitempty"`
	Targets      []EnvironmentActionTarget `json:"targets"`
	Skipped      []EnvironmentActionTarget `json:"skipped"`
	Total        int                       `json:"total"`
}

type EnvironmentActionResult struct {
	Plan    EnvironmentActionPlan     `json:"plan"`
	Applied []EnvironmentActionTarget `json:"applied"`
	Skipped []EnvironmentActionTarget `json:"skipped"`
}

type EnvironmentActionTarget struct {
	ID             string    `json:"id"`
	Profile        string    `json:"profile,omitempty"`
	Backend        string    `json:"backend,omitempty"`
	Status         string    `json:"status,omitempty"`
	Workspace      string    `json:"workspace,omitempty"`
	GuestWorkspace string    `json:"guestWorkspace,omitempty"`
	InstanceName   string    `json:"instanceName,omitempty"`
	LastSessionID  string    `json:"lastSessionId,omitempty"`
	LastCommand    string    `json:"lastCommand,omitempty"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	LastStartedAt  time.Time `json:"lastStartedAt,omitempty"`
	LastEndedAt    time.Time `json:"lastEndedAt,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}

type EnvironmentOperator interface {
	StopInstance(context.Context, string) error
	Cleanup(context.Context, *backend.Session) error
}

type EnvironmentApplyOptions struct {
	Operator EnvironmentOperator
}

func (c Core) PlanEnvironmentStop(opts EnvironmentActionOptions) (EnvironmentActionPlan, error) {
	return c.planEnvironmentAction(EnvironmentActionStop, opts)
}

func (c Core) ApplyEnvironmentStop(ctx context.Context, plan EnvironmentActionPlan, opts EnvironmentApplyOptions) (EnvironmentActionResult, error) {
	if plan.Action != EnvironmentActionStop {
		return EnvironmentActionResult{Plan: plan}, fmt.Errorf("environment action plan is %q, not %q", plan.Action, EnvironmentActionStop)
	}
	store, err := c.environmentStore()
	if err != nil {
		return EnvironmentActionResult{Plan: plan}, err
	}
	operator := environmentOperatorOrDefault(opts.Operator)
	result := EnvironmentActionResult{Plan: plan, Applied: []EnvironmentActionTarget{}, Skipped: append([]EnvironmentActionTarget(nil), plan.Skipped...)}
	for _, target := range plan.Targets {
		applied, skipped, err := applyEnvironmentStopTarget(ctx, store, operator, target.ID)
		if err != nil {
			return result, err
		}
		if skipped.Reason != "" {
			result.Skipped = append(result.Skipped, skipped)
			continue
		}
		result.Applied = append(result.Applied, applied)
	}
	return result, nil
}

func (c Core) PlanEnvironmentClean(opts EnvironmentActionOptions) (EnvironmentActionPlan, error) {
	return c.planEnvironmentAction(EnvironmentActionClean, opts)
}

func (c Core) ApplyEnvironmentClean(ctx context.Context, plan EnvironmentActionPlan, opts EnvironmentApplyOptions) (EnvironmentActionResult, error) {
	if plan.Action != EnvironmentActionClean {
		return EnvironmentActionResult{Plan: plan}, fmt.Errorf("environment action plan is %q, not %q", plan.Action, EnvironmentActionClean)
	}
	store, err := c.environmentStore()
	if err != nil {
		return EnvironmentActionResult{Plan: plan}, err
	}
	operator := environmentOperatorOrDefault(opts.Operator)
	result := EnvironmentActionResult{Plan: plan, Applied: []EnvironmentActionTarget{}, Skipped: append([]EnvironmentActionTarget(nil), plan.Skipped...)}
	for _, target := range plan.Targets {
		applied, err := applyEnvironmentCleanTarget(ctx, store, operator, target.ID)
		if err != nil {
			return result, err
		}
		result.Applied = append(result.Applied, applied)
	}
	return result, nil
}

func (c Core) planEnvironmentAction(action string, opts EnvironmentActionOptions) (EnvironmentActionPlan, error) {
	store, err := c.environmentStore()
	if err != nil {
		return EnvironmentActionPlan{Action: action}, err
	}
	records, err := selectEnvironmentRecords(store, opts.IDs)
	if err != nil {
		return EnvironmentActionPlan{Action: action}, err
	}
	records = filterEnvironmentActionRecords(records, opts)
	plan := EnvironmentActionPlan{
		Action:       action,
		RequestedIDs: cleanEnvironmentIDs(opts.IDs),
		Filter:       environmentActionFilter(opts),
		Targets:      []EnvironmentActionTarget{},
		Skipped:      []EnvironmentActionTarget{},
		Total:        len(records),
	}
	for _, rec := range records {
		target := environmentActionTargetFromRecord(rec, "")
		switch action {
		case EnvironmentActionStop:
			switch {
			case rec.Status == "stopped":
				target.Reason = "already-stopped"
				plan.Skipped = append(plan.Skipped, target)
			case rec.Backend != "lima" || strings.TrimSpace(rec.InstanceName) == "":
				target.Reason = "no-lima-instance"
				plan.Skipped = append(plan.Skipped, target)
			default:
				plan.Targets = append(plan.Targets, target)
			}
		case EnvironmentActionClean:
			plan.Targets = append(plan.Targets, target)
		default:
			return plan, fmt.Errorf("unknown environment action %q", action)
		}
	}
	return plan, nil
}

func (c Core) environmentStore() (environment.Store, error) {
	if c.Store.Root == "" {
		return environment.Store{}, errors.New("manager store root is required")
	}
	return environment.Store{Root: c.Store.Root}, nil
}

func selectEnvironmentRecords(store environment.Store, ids []string) ([]environment.Record, error) {
	ids = cleanEnvironmentIDs(ids)
	if len(ids) == 0 {
		return store.List()
	}
	records := make([]environment.Record, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		rec, err := store.Load(id)
		if err != nil {
			return nil, err
		}
		if seen[rec.ID] {
			continue
		}
		seen[rec.ID] = true
		records = append(records, rec)
	}
	return records, nil
}

func cleanEnvironmentIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func filterEnvironmentActionRecords(records []environment.Record, opts EnvironmentActionOptions) []environment.Record {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := records[:0]
	for _, rec := range records {
		if opts.StoppedOnly && rec.Status != "stopped" {
			continue
		}
		if opts.IdleSet {
			if rec.LastEndedAt.IsZero() || now.Sub(rec.LastEndedAt) < opts.Idle {
				continue
			}
		}
		out = append(out, rec)
	}
	return out
}

func environmentActionFilter(opts EnvironmentActionOptions) EnvironmentActionFilter {
	filter := EnvironmentActionFilter{StoppedOnly: opts.StoppedOnly}
	if opts.IdleSet {
		filter.Idle = opts.Idle.String()
	}
	return filter
}

func environmentActionTargetFromRecord(rec environment.Record, reason string) EnvironmentActionTarget {
	return EnvironmentActionTarget{
		ID:             rec.ID,
		Profile:        rec.Profile,
		Backend:        rec.Backend,
		Status:         rec.Status,
		Workspace:      rec.Workspace,
		GuestWorkspace: rec.GuestWorkspace,
		InstanceName:   rec.InstanceName,
		LastSessionID:  rec.LastSessionID,
		LastCommand:    rec.LastCommand,
		CreatedAt:      rec.CreatedAt,
		LastStartedAt:  rec.LastStartedAt,
		LastEndedAt:    rec.LastEndedAt,
		Reason:         reason,
	}
}

func applyEnvironmentStopTarget(ctx context.Context, store environment.Store, operator EnvironmentOperator, id string) (EnvironmentActionTarget, EnvironmentActionTarget, error) {
	lock, err := store.Lock(id)
	if err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	defer lock.Unlock()
	rec, err := store.Load(id)
	if err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	switch {
	case rec.Status == "stopped":
		return EnvironmentActionTarget{}, environmentActionTargetFromRecord(rec, "already-stopped"), nil
	case rec.Backend != "lima" || strings.TrimSpace(rec.InstanceName) == "":
		return EnvironmentActionTarget{}, environmentActionTargetFromRecord(rec, "no-lima-instance"), nil
	}
	if err := operator.StopInstance(ctx, rec.InstanceName); err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	rec.Status = "stopped"
	if err := store.Save(rec); err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	return environmentActionTargetFromRecord(rec, ""), EnvironmentActionTarget{}, nil
}

func applyEnvironmentCleanTarget(ctx context.Context, store environment.Store, operator EnvironmentOperator, id string) (EnvironmentActionTarget, error) {
	lock, err := store.Lock(id)
	if err != nil {
		return EnvironmentActionTarget{}, err
	}
	defer lock.Unlock()
	rec, err := store.Load(id)
	if err != nil {
		return EnvironmentActionTarget{}, err
	}
	target := environmentActionTargetFromRecord(rec, "")
	if rec.Backend == "lima" && strings.TrimSpace(rec.InstanceName) != "" {
		if err := operator.Cleanup(ctx, &backend.Session{InstanceName: rec.InstanceName}); err != nil {
			return EnvironmentActionTarget{}, err
		}
	}
	if err := store.Remove(rec.ID); err != nil {
		return EnvironmentActionTarget{}, err
	}
	return target, nil
}

func environmentOperatorOrDefault(operator EnvironmentOperator) EnvironmentOperator {
	if operator != nil {
		return operator
	}
	return lima.Backend{Stdout: io.Discard, Stderr: io.Discard}
}

func sortEnvironmentTargetsByID(targets []EnvironmentActionTarget) {
	slices.SortFunc(targets, func(a, b EnvironmentActionTarget) int {
		return strings.Compare(a.ID, b.ID)
	})
}
