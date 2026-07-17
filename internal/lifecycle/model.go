package lifecycle

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

type ResourceKind string
type KindStatus string
type ResourceState string
type PersistenceClass string
type ClosePolicy string
type StopMode string
type FactClass string

const (
	KindImplemented KindStatus = "implemented"
	KindDesignReady KindStatus = "design-ready"
	KindFixtureOnly KindStatus = "fixture-only"

	StatePlanned  ResourceState = "planned"
	StateStarting ResourceState = "starting"
	StateActive   ResourceState = "active"
	StateDraining ResourceState = "draining"
	StateReleased ResourceState = "released"
	StateFailed   ResourceState = "failed"
	StateOrphaned ResourceState = "orphaned"

	PersistenceEphemeral PersistenceClass = "ephemeral"
	PersistenceRetained  PersistenceClass = "retained"
	PersistenceEvidence  PersistenceClass = "evidence"

	ClosePreStopDrain        ClosePolicy = "pre-stop-drain"
	CloseCoTerminateWithRoot ClosePolicy = "co-terminate-with-root"
	CloseSurviveRoot         ClosePolicy = "survive-root"
	CloseExternalUnmanaged   ClosePolicy = "external-unmanaged"

	StopModePin   StopMode = "pin"
	StopModeDrain StopMode = "drain"

	FactRetained FactClass = "retained"
	FactHandoff  FactClass = "handoff"
)

var (
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	bootIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

type EnvironmentRef struct {
	EnvironmentID   string `json:"environmentId"`
	StartGeneration uint64 `json:"startGeneration"`
	InstanceName    string `json:"instanceName"`
	BootID          string `json:"bootId,omitempty"`
}

func (r EnvironmentRef) Validate(active bool) error {
	if !idPattern.MatchString(r.EnvironmentID) {
		return errors.New("environment reference id is invalid")
	}
	if r.StartGeneration == 0 {
		return errors.New("environment start generation is required")
	}
	if !idPattern.MatchString(r.InstanceName) {
		return errors.New("environment instance name is invalid")
	}
	if active && !bootIDPattern.MatchString(r.BootID) {
		return errors.New("active environment boot id is invalid")
	}
	if !active && r.BootID != "" && !bootIDPattern.MatchString(r.BootID) {
		return errors.New("environment boot id is invalid")
	}
	return nil
}

func (r EnvironmentRef) Equal(other EnvironmentRef) bool {
	return r.EnvironmentID == other.EnvironmentID &&
		r.StartGeneration == other.StartGeneration &&
		r.InstanceName == other.InstanceName && r.BootID == other.BootID
}

type ResourceRef struct {
	Kind       ResourceKind `json:"kind"`
	ID         string       `json:"id"`
	Generation uint64       `json:"generation"`
}

func (r ResourceRef) Validate() error {
	if !idPattern.MatchString(string(r.Kind)) || !idPattern.MatchString(r.ID) {
		return errors.New("resource reference is invalid")
	}
	if r.Generation == 0 {
		return errors.New("resource generation is required")
	}
	return nil
}

func (r ResourceRef) Key() string {
	return fmt.Sprintf("%s/%s/%d", r.Kind, r.ID, r.Generation)
}

type OwnerRef struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Generation uint64 `json:"generation"`
}

func (r OwnerRef) Validate() error {
	if !idPattern.MatchString(r.Kind) || !idPattern.MatchString(r.ID) || r.Generation == 0 {
		return errors.New("resource owner is invalid")
	}
	return nil
}

type DependencySpec struct {
	Ref      ResourceRef `json:"ref"`
	StopMode StopMode    `json:"stopMode"`
}

type Resource struct {
	Ref                  ResourceRef      `json:"ref"`
	Owner                OwnerRef         `json:"owner"`
	State                ResourceState    `json:"state"`
	Dependencies         []DependencySpec `json:"dependencies,omitempty"`
	Persistence          PersistenceClass `json:"persistence"`
	ClosePolicy          ClosePolicy      `json:"closePolicy"`
	Incarnation          *EnvironmentRef  `json:"incarnation,omitempty"`
	PossibleVMDependency bool             `json:"possibleVmDependency,omitempty"`
	UpdatedAt            time.Time        `json:"updatedAt"`
}

// Fact is a bounded, non-authoritative classification of product state whose
// real authority lives in another provider store or append-only audit. Facts
// never participate in the dependency graph or stop predicate.
type Fact struct {
	Kind       ResourceKind `json:"kind"`
	ID         string       `json:"id"`
	Class      FactClass    `json:"class"`
	Generation uint64       `json:"generation"`
	RecordedAt time.Time    `json:"recordedAt"`
}

func (r Resource) IsPossiblyLive() bool {
	return slices.Contains([]ResourceState{StatePlanned, StateStarting, StateActive, StateDraining, StateOrphaned}, r.State)
}

func ValidateTransition(from, to ResourceState) error {
	if from == to {
		return nil
	}
	allowed := map[ResourceState][]ResourceState{
		StatePlanned:  {StateStarting, StateFailed, StateOrphaned},
		StateStarting: {StateActive, StateFailed, StateOrphaned},
		StateActive:   {StateDraining, StateFailed, StateOrphaned},
		StateDraining: {StateReleased, StateFailed, StateOrphaned},
		StateOrphaned: {StateDraining},
	}
	if !slices.Contains(allowed[from], to) {
		return fmt.Errorf("invalid lifecycle transition %q -> %q", from, to)
	}
	return nil
}

func validState(state ResourceState) bool {
	return slices.Contains([]ResourceState{StatePlanned, StateStarting, StateActive, StateDraining, StateReleased, StateFailed, StateOrphaned}, state)
}

func boundedReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}
