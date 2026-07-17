package lifecycle

import (
	"errors"
	"fmt"
	"sort"

	"github.com/vibe-agi/hideout/internal/backend"
)

type EvaluationInput struct {
	Incarnation              EnvironmentRef
	Resources                []Resource
	Observation              backend.LifecycleObservation
	TransitionInFlight       bool
	GraceExpired             bool
	ReconciliationComplete   bool
	CurrentDaemonOwnsAttempt bool
}

type Evaluation struct {
	Allowed bool
	Pins    []ResourceRef
	Drains  []ResourceRef
	Reasons []string
}

func ValidateGraph(resources []Resource, production bool) error {
	byKey := make(map[string]Resource, len(resources))
	for _, resource := range resources {
		if err := validateResource(resource, production); err != nil {
			return fmt.Errorf("resource %s: %w", resource.Ref.Key(), err)
		}
		key := resource.Ref.Key()
		if _, exists := byKey[key]; exists {
			return fmt.Errorf("duplicate lifecycle resource %s", key)
		}
		byKey[key] = resource
	}
	for _, resource := range resources {
		for _, dependency := range resource.Dependencies {
			if _, ok := byKey[dependency.Ref.Key()]; !ok {
				return fmt.Errorf("resource %s has missing dependency %s", resource.Ref.Key(), dependency.Ref.Key())
			}
		}
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return errors.New("lifecycle dependency graph contains a cycle")
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, dependency := range byKey[key].Dependencies {
			if err := visit(dependency.Ref.Key()); err != nil {
				return err
			}
		}
		delete(visiting, key)
		visited[key] = true
		return nil
	}
	for key := range byKey {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

func EvaluateAutoStop(input EvaluationInput) (Evaluation, error) {
	if err := input.Incarnation.Validate(true); err != nil {
		return Evaluation{}, err
	}
	if err := input.Observation.Validate(); err != nil {
		return Evaluation{}, err
	}
	if err := ValidateGraph(input.Resources, true); err != nil {
		return Evaluation{}, err
	}
	byKey := make(map[string]Resource, len(input.Resources))
	rootKey := ""
	for _, resource := range input.Resources {
		byKey[resource.Ref.Key()] = resource
		if resource.Ref.Kind == KindBackendIncarnation && resource.Incarnation != nil && resource.Incarnation.Equal(input.Incarnation) {
			rootKey = resource.Ref.Key()
		}
	}
	if rootKey == "" {
		return Evaluation{}, errors.New("current backend incarnation resource is missing")
	}
	evaluation := Evaluation{}
	for _, resource := range input.Resources {
		if !resource.IsPossiblyLive() || resource.Ref.Key() == rootKey {
			continue
		}
		mode, found := pathMode(resource.Ref.Key(), rootKey, byKey, map[string]bool{})
		if resource.State == StateOrphaned && (found || resource.PossibleVMDependency) {
			evaluation.Pins = append(evaluation.Pins, resource.Ref)
			continue
		}
		if !found {
			continue
		}
		if mode == StopModePin {
			evaluation.Pins = append(evaluation.Pins, resource.Ref)
		} else {
			evaluation.Drains = append(evaluation.Drains, resource.Ref)
		}
	}
	sort.Slice(evaluation.Pins, func(i, j int) bool { return evaluation.Pins[i].Key() < evaluation.Pins[j].Key() })
	sort.Slice(evaluation.Drains, func(i, j int) bool { return evaluation.Drains[i].Key() < evaluation.Drains[j].Key() })
	if input.Observation.State != backend.LifecycleRunning || input.Observation.InstanceName != input.Incarnation.InstanceName || input.Observation.BootID != input.Incarnation.BootID {
		evaluation.Reasons = append(evaluation.Reasons, "backend-incarnation-not-observed-running")
	}
	if len(evaluation.Pins) != 0 {
		evaluation.Reasons = append(evaluation.Reasons, "vm-dependency-live-or-unproved")
	}
	if len(evaluation.Drains) != 0 {
		evaluation.Reasons = append(evaluation.Reasons, "pre-stop-drain-live-or-unproved")
	}
	if input.TransitionInFlight {
		evaluation.Reasons = append(evaluation.Reasons, "lifecycle-transition-in-flight")
	}
	if !input.GraceExpired {
		evaluation.Reasons = append(evaluation.Reasons, "idle-grace-not-expired")
	}
	if !input.ReconciliationComplete {
		evaluation.Reasons = append(evaluation.Reasons, "reconciliation-incomplete")
	}
	if !input.CurrentDaemonOwnsAttempt {
		evaluation.Reasons = append(evaluation.Reasons, "stop-attempt-not-owned")
	}
	evaluation.Allowed = len(evaluation.Reasons) == 0
	return evaluation, nil
}

func pathMode(current, root string, resources map[string]Resource, seen map[string]bool) (StopMode, bool) {
	if seen[current] {
		return "", false
	}
	seen[current] = true
	defer delete(seen, current)
	best := StopMode("")
	for _, dependency := range resources[current].Dependencies {
		mode := dependency.StopMode
		found := dependency.Ref.Key() == root
		if !found {
			childMode, childFound := pathMode(dependency.Ref.Key(), root, resources, seen)
			found = childFound
			if childMode == StopModePin {
				mode = StopModePin
			}
		}
		if !found {
			continue
		}
		if mode == StopModePin {
			return StopModePin, true
		}
		best = StopModeDrain
	}
	return best, best != ""
}
