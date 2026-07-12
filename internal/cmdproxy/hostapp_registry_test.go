package cmdproxy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestReservedHostAppCommandsAreCoreOwnedAndDeterministic(t *testing.T) {
	first := ReservedHostAppCommands()
	second := ReservedHostAppCommands()
	if len(first) == 0 || !reflect.DeepEqual(first, second) {
		t.Fatalf("reserved catalog is empty or non-deterministic: first=%+v second=%+v", first, second)
	}
	for i, entry := range first {
		if entry.Name == "" || entry.Class == "" || entry.Reason == "" {
			t.Fatalf("incomplete reserved command at %d: %+v", i, entry)
		}
		if i > 0 && first[i-1].Name >= entry.Name {
			t.Fatalf("reserved catalog is not strictly sorted: %q before %q", first[i-1].Name, entry.Name)
		}
	}
	for _, name := range []string{"hideout", "hideout-shim", "open", "sh", "env"} {
		entry, ok := LookupReservedHostAppCommand(name)
		if !ok || entry.Name != name {
			t.Fatalf("Core command %q is not reserved: %+v ok=%v", name, entry, ok)
		}
	}
	first[0].Reason = "mutated"
	if reflect.DeepEqual(first, ReservedHostAppCommands()) {
		t.Fatal("caller mutated the Core-owned reserved catalog")
	}
}

func TestPlanHostAppCommandOwnersRejectsReservedNames(t *testing.T) {
	for _, name := range []string{"hideout", "hideout-shim", "open", "sh"} {
		_, err := PlanHostAppCommandOwners(nil, []HostAppCommandOwner{{Command: name, Owner: "community.editor/binding"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("reserved command %q was not rejected: %v", name, err)
		}
	}
	_, err := PlanHostAppCommandOwners(
		[]HostAppCommandOwner{{Command: "open", Owner: "core/host-open"}},
		[]HostAppCommandOwner{{Command: "open", Owner: "community.editor/binding"}},
		[]HostAppOwnerReplacement{{Command: "open", FromOwner: "core/host-open", ToOwner: "community.editor/binding"}},
	)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("explicit replacement overrode a reserved command: %v", err)
	}
}

func TestPlanHostAppCommandOwnersRequiresExactReplacement(t *testing.T) {
	current := []HostAppCommandOwner{
		{Command: "cursor", Owner: "builtin.cursor/default"},
		{Command: "zed", Owner: "community.zed/stable"},
	}
	requested := []HostAppCommandOwner{
		{Command: "cursor", Owner: "community.cursor/nightly"},
		{Command: "subl", Owner: "community.sublime/default"},
	}

	if _, err := PlanHostAppCommandOwners(current, requested, nil); err == nil || !strings.Contains(err.Error(), "explicit owner replacement") {
		t.Fatalf("implicit precedence was accepted: %v", err)
	}
	for _, replacement := range []HostAppOwnerReplacement{
		{Command: "cursor", FromOwner: "wrong-owner", ToOwner: "community.cursor/nightly"},
		{Command: "cursor", FromOwner: "builtin.cursor/default", ToOwner: "wrong-owner"},
		{Command: "subl", FromOwner: "missing-owner", ToOwner: "community.sublime/default"},
	} {
		if _, err := PlanHostAppCommandOwners(current, requested, []HostAppOwnerReplacement{replacement}); err == nil {
			t.Fatalf("stale or unused replacement was accepted: %+v", replacement)
		}
	}

	plan, err := PlanHostAppCommandOwners(current, requested, []HostAppOwnerReplacement{{
		Command:   "cursor",
		FromOwner: "builtin.cursor/default",
		ToOwner:   "community.cursor/nightly",
	}})
	if err != nil {
		t.Fatalf("exact replacement failed: %v", err)
	}
	wantOwners := []HostAppCommandOwner{
		{Command: "cursor", Owner: "community.cursor/nightly"},
		{Command: "subl", Owner: "community.sublime/default"},
		{Command: "zed", Owner: "community.zed/stable"},
	}
	if !reflect.DeepEqual(plan.Owners, wantOwners) {
		t.Fatalf("owners=%+v want %+v", plan.Owners, wantOwners)
	}
	if !reflect.DeepEqual(plan.Replacements, []HostAppOwnerReplacement{{
		Command:   "cursor",
		FromOwner: "builtin.cursor/default",
		ToOwner:   "community.cursor/nightly",
	}}) {
		t.Fatalf("replacements=%+v", plan.Replacements)
	}
}

func TestPlanHostAppCommandOwnersIsOrderIndependentAndIdempotent(t *testing.T) {
	currentA := []HostAppCommandOwner{
		{Command: "zed", Owner: "community.zed/stable"},
		{Command: "cursor", Owner: "community.cursor/stable"},
	}
	requestedA := []HostAppCommandOwner{
		{Command: "subl", Owner: "community.sublime/default"},
		{Command: "cursor", Owner: "community.cursor/stable"},
	}
	currentB := []HostAppCommandOwner{currentA[1], currentA[0]}
	requestedB := []HostAppCommandOwner{requestedA[1], requestedA[0]}

	a, err := PlanHostAppCommandOwners(currentA, requestedA, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PlanHostAppCommandOwners(currentB, requestedB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("input order changed plan:\nA=%+v\nB=%+v", a, b)
	}
	if len(a.Replacements) != 0 {
		t.Fatalf("same-owner idempotent claim became replacement: %+v", a.Replacements)
	}

	_, err = PlanHostAppCommandOwners(nil, []HostAppCommandOwner{
		{Command: "cursor", Owner: "owner-a"},
		{Command: "cursor", Owner: "owner-b"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "multiple requested owners") {
		t.Fatalf("ambiguous requested owners were accepted: %v", err)
	}
}

func TestPlanHostAppCommandOwnersRejectsSixtyFifthCommandAcrossPacks(t *testing.T) {
	var current []HostAppCommandOwner
	for pack := 0; pack < 4; pack++ {
		requested := make([]HostAppCommandOwner, 0, 16)
		for command := 0; command < 16; command++ {
			requested = append(requested, HostAppCommandOwner{
				Command: fmt.Sprintf("tool-%d-%02d", pack, command),
				Owner:   fmt.Sprintf("community.pack-%d/binding-%02d", pack, command),
			})
		}
		plan, err := PlanHostAppCommandOwners(current, requested, nil)
		if err != nil {
			t.Fatalf("merge pack %d: %v", pack, err)
		}
		current = plan.Owners
	}
	if len(current) != MaxProjectedHostAppCommands {
		t.Fatalf("combined profile has %d commands, want %d", len(current), MaxProjectedHostAppCommands)
	}

	plan, err := PlanHostAppCommandOwners(current, []HostAppCommandOwner{{
		Command: "tool-4-00",
		Owner:   "community.pack-4/binding-00",
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeding limit 64") {
		t.Fatalf("sixty-fifth combined command was accepted: plan=%+v err=%v", plan, err)
	}
	if len(plan.Owners) != 0 {
		t.Fatalf("failed plan exposed a partial ownership result: %+v", plan)
	}
}
