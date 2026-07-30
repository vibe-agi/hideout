package manager

import (
	"context"
	"errors"
	"testing"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityOwnerSelectorRequiresOneExactOwnerForm(t *testing.T) {
	for _, selector := range []ActivityOwnerSelector{
		{},
		{EnvironmentID: "env_fixture"},
		{BackendIncarnationID: "incarnation-a"},
		{
			EnvironmentID: "env_fixture", BackendIncarnationID: "incarnation-a",
			SessionID: "ses_conflict",
		},
		{SessionID: "not-a-session"},
		{EnvironmentID: "bad", BackendIncarnationID: "incarnation-a"},
	} {
		if err := selector.Validate(); !errors.Is(err, ErrActivityQueryInvalid) {
			t.Fatalf("selector=%+v error=%v", selector, err)
		}
	}
	for _, selector := range []ActivityOwnerSelector{
		{EnvironmentID: "env_fixture", BackendIncarnationID: "incarnation-a"},
		{SessionID: "ses_20260729T120000Z_fixture"},
	} {
		if err := selector.Validate(); err != nil {
			t.Fatalf("selector=%+v error=%v", selector, err)
		}
	}
}

func TestActivityServiceRejectsResolverOwnerRebinding(t *testing.T) {
	requested := ActivityOwnerSelector{
		EnvironmentID: "env_fixture", BackendIncarnationID: "incarnation-a",
	}
	foreign, err := workloadtypes.NewReusableOwner(
		"env_fixture", "lima", "incarnation-b",
	)
	if err != nil {
		t.Fatal(err)
	}
	service := ActivityService{
		OwnerResolver: ActivityOwnerResolverFunc(
			func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
				return foreign, nil
			},
		),
	}
	if _, err := service.ResolveActivityOwner(context.Background(), requested); err == nil {
		t.Fatal("resolver rebound exact owner without rejection")
	}

	exact, err := workloadtypes.NewReusableOwner(
		"env_fixture", "lima", "incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	service.OwnerResolver = ActivityOwnerResolverFunc(
		func(context.Context, ActivityOwnerSelector) (workloadtypes.ActivityOwner, error) {
			return exact, nil
		},
	)
	resolved, err := service.ResolveActivityOwner(context.Background(), requested)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Equal(exact) {
		t.Fatalf("resolved=%+v want=%+v", resolved, exact)
	}
}
