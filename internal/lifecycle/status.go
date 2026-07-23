package lifecycle

import (
	"sort"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
)

const StatusSchema = "hideout.lifecycle-status/v1"

type Activity string

const (
	ActivityPinned          Activity = "pinned"
	ActivityIdleGrace       Activity = "idle-grace"
	ActivityIdleEligible    Activity = "idle-stop-eligible"
	ActivityBlocked         Activity = "blocked-unproved"
	ActivityStopping        Activity = "stopping"
	ActivityStoppingUnknown Activity = "stopping-unknown"
	ActivityEstablishing    Activity = "establishing"
	ActivityStopped         Activity = "stopped"
	ActivityNotApplicable   Activity = "not-applicable"
)

type ResourceSummary struct {
	Kind  ResourceKind  `json:"kind"`
	ID    string        `json:"id"`
	State ResourceState `json:"state"`
}

type Status struct {
	Schema               string            `json:"schema"`
	EnvironmentID        string            `json:"environmentId"`
	StartGeneration      uint64            `json:"startGeneration,omitempty"`
	BackendState         string            `json:"backendState"`
	BackendObservedAt    time.Time         `json:"backendObservedAt"`
	Activity             Activity          `json:"activity"`
	IdleDeadline         *time.Time        `json:"idleDeadline,omitempty"`
	ReasonCode           string            `json:"reasonCode,omitempty"`
	Reconciliation       string            `json:"reconciliation"`
	DisposalPhase        string            `json:"disposalPhase,omitempty"`
	DisposalReasonCode   string            `json:"disposalReasonCode,omitempty"`
	Pins                 []ResourceSummary `json:"pins,omitempty"`
	Drains               []ResourceSummary `json:"drains,omitempty"`
	Retained             []ResourceSummary `json:"retained,omitempty"`
	Handoffs             []ResourceSummary `json:"handoffs,omitempty"`
	Orphans              []ResourceSummary `json:"orphans,omitempty"`
	EstablishingSessions int               `json:"establishingSessions,omitempty"`
}

func BuildStatus(environmentID string, generation uint64, observation backend.LifecycleObservation, resources []Resource, facts []Fact, deadline *time.Time, reconciliation Reconciliation, stopState string) Status {
	status := Status{
		Schema: StatusSchema, EnvironmentID: audit.RedactString(environmentID),
		StartGeneration: generation, BackendState: string(observation.State),
		BackendObservedAt: observation.ObservedAt.UTC(),
		Reconciliation:    audit.RedactString(reconciliation.State),
	}
	byKey := make(map[string]Resource, len(resources))
	rootKey := ""
	for _, resource := range resources {
		byKey[resource.Ref.Key()] = resource
		if resource.Ref.Kind == KindBackendIncarnation {
			rootKey = resource.Ref.Key()
		}
	}
	for _, resource := range resources {
		summary := ResourceSummary{Kind: resource.Ref.Kind, ID: audit.RedactString(resource.Ref.ID), State: resource.State}
		switch {
		case resource.State == StateOrphaned:
			status.Orphans = append(status.Orphans, summary)
		}
		if rootKey == "" || resource.Ref.Key() == rootKey || !resource.IsPossiblyLive() {
			continue
		}
		mode, found := pathMode(resource.Ref.Key(), rootKey, byKey, map[string]bool{})
		if !found {
			continue
		}
		if mode == StopModePin || resource.State == StateOrphaned {
			status.Pins = append(status.Pins, summary)
		} else {
			status.Drains = append(status.Drains, summary)
		}
	}
	for _, fact := range facts {
		summary := ResourceSummary{Kind: fact.Kind, ID: audit.RedactString(fact.ID), State: StateReleased}
		switch fact.Class {
		case FactHandoff:
			status.Handoffs = append(status.Handoffs, summary)
		case FactRetained:
			status.Retained = append(status.Retained, summary)
		}
	}
	if observation.State == "not-applicable" {
		status.Activity = ActivityNotApplicable
	} else if stopState == "unknown" {
		status.Activity = ActivityStoppingUnknown
		status.ReasonCode = audit.RedactString(observation.ReasonCode)
	} else if stopState == "planned" || stopState == "draining" || stopState == "invoked" || stopState == "observing" {
		status.Activity = ActivityStopping
	} else if observation.State == backend.LifecycleUnknown || reconciliation.State != "complete" || len(status.Orphans) != 0 {
		status.Activity = ActivityBlocked
	} else if stopState == "committed" || observation.State == backend.LifecycleStopped || observation.State == backend.LifecycleAbsent {
		status.Activity = ActivityStopped
	} else if len(status.Pins) != 0 || len(status.Drains) != 0 {
		status.Activity = ActivityPinned
	} else if deadline != nil {
		status.Activity = ActivityIdleGrace
	} else {
		status.Activity = ActivityIdleEligible
	}
	if status.ReasonCode == "" && reconciliation.State != "complete" {
		status.ReasonCode = audit.RedactString(reconciliation.ReasonCode)
	}
	if status.ReasonCode == "" && observation.State == backend.LifecycleUnknown {
		status.ReasonCode = audit.RedactString(observation.ReasonCode)
	}
	if status.ReasonCode == "" && status.Activity == ActivityBlocked {
		status.ReasonCode = "reconciliation-unproved"
	}
	status.IdleDeadline = deadline
	for _, list := range []*[]ResourceSummary{&status.Pins, &status.Drains, &status.Retained, &status.Handoffs, &status.Orphans} {
		sort.Slice(*list, func(i, j int) bool {
			return string((*list)[i].Kind)+(*list)[i].ID < string((*list)[j].Kind)+(*list)[j].ID
		})
	}
	return status
}

func sortStatuses(values []Status) {
	sort.Slice(values, func(i, j int) bool { return values[i].EnvironmentID < values[j].EnvironmentID })
}
