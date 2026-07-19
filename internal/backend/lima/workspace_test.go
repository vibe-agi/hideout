package lima

import (
	"testing"
)

func TestWorkspacePortalGuestEndpointUsesLimaHostAlias(t *testing.T) {
	got, err := WorkspacePortalGuestEndpoint("127.0.0.1:43127")
	if err != nil {
		t.Fatal(err)
	}
	if want := "host.lima.internal:43127"; got != want {
		t.Fatalf("guest endpoint=%q want %q", got, want)
	}
}

func TestWorkspacePortalGuestEndpointRejectsNonLoopbackAndUnassignedPorts(t *testing.T) {
	for _, address := range []string{
		"0.0.0.0:43127",
		"192.0.2.5:43127",
		"127.0.0.1:0",
		"not-an-address",
	} {
		t.Run(address, func(t *testing.T) {
			if _, err := WorkspacePortalGuestEndpoint(address); err == nil {
				t.Fatalf("expected %q to be rejected", address)
			}
		})
	}
}

func TestNewWorkspaceProviderFactoryDoesNotRequireProjectFacts(t *testing.T) {
	if factory := NewWorkspaceProviderFactory(); factory == nil {
		t.Fatal("expected a workspace provider factory")
	}
}
