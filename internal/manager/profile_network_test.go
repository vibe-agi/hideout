package manager

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestProfileNetworkPlanApplyPreservesReusableProxyConfiguration(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	proxyPlan, err := core.PlanProfileNetwork(ProfileNetworkOptions{
		ProfileName:      "default",
		Mode:             profile.NetworkModeTun2Socks,
		ProxySecretRef:   "charles",
		MediatedResolver: "1.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !proxyPlan.Changed || proxyPlan.After.Mode != profile.NetworkModeTun2Socks || proxyPlan.After.ProxySecretRef != "charles" {
		t.Fatalf("unexpected proxy plan: %+v", proxyPlan)
	}
	if _, err := store.Load("default"); err == nil {
		t.Fatal("planning created profile state")
	}
	if _, err := core.ApplyProfileNetwork(proxyPlan); err != nil {
		t.Fatal(err)
	}

	directPlan, err := core.PlanProfileNetwork(ProfileNetworkOptions{ProfileName: "default", Mode: profile.NetworkModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	if directPlan.After.ProxySecretRef != "charles" || directPlan.After.MediatedResolver != "1.1.1.1" {
		t.Fatalf("direct plan forgot reusable proxy configuration: %+v", directPlan.After)
	}
	if _, err := core.ApplyProfileNetwork(directPlan); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Network.Mode != profile.NetworkModeDirect || loaded.Network.ProxySecretRef != "charles" {
		t.Fatalf("stored network mismatch: %+v", loaded.Network)
	}

	runtimeConfig, err := RuntimeConfigurationForProfile(loaded, "lima", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeConfig.Services.Network.Egress != "direct" || runtimeConfig.Services.Network.ProxySecretRef != "" || runtimeConfig.Services.Network.Resolver != "" {
		t.Fatalf("inactive proxy material entered service configuration: %+v", runtimeConfig.Services.Network)
	}

	reusePlan, err := core.PlanProfileNetwork(ProfileNetworkOptions{
		ProfileName:    "default",
		Mode:           profile.NetworkModeTun2Socks,
		ProxySecretRef: "next-proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reusePlan.After.MediatedResolver != "1.1.1.1" {
		t.Fatalf("saved resolver was not reused: %+v", reusePlan.After)
	}
}

func TestProfileNetworkApplyRejectsPlanDrift(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	p := profile.Default("default")
	if err := store.Create(p); err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanProfileNetwork(ProfileNetworkOptions{
		ProfileName:      "default",
		Mode:             profile.NetworkModeTun2Socks,
		ProxySecretRef:   "planned-proxy",
		MediatedResolver: "1.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err = store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	p.Network = profile.Network{Mode: profile.NetworkModeTun2Socks, ProxySecretRef: "other-proxy", MediatedResolver: "9.9.9.9"}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyProfileNetwork(plan); err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("expected stale-plan rejection, got %v", err)
	}
}

func TestProfileNetworkNoopDoesNotCreateProfile(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanProfileNetwork(ProfileNetworkOptions{ProfileName: "default", Mode: profile.NetworkModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed {
		t.Fatalf("default direct plan changed: %+v", plan)
	}
	result, err := core.ApplyProfileNetwork(plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied {
		t.Fatalf("no-op plan applied: %+v", result)
	}
	if _, err := store.Load("default"); err == nil {
		t.Fatal("no-op apply created profile state")
	}
}

func TestProfileNetworkProxyRequiresResolverOnFirstUse(t *testing.T) {
	core := New(profile.Store{Root: t.TempDir()})
	_, err := core.PlanProfileNetwork(ProfileNetworkOptions{
		ProfileName:    "default",
		Mode:           profile.NetworkModeTun2Socks,
		ProxySecretRef: "charles",
	})
	if err == nil || !strings.Contains(err.Error(), "connect through charles using <resolver>") {
		t.Fatalf("unexpected resolver error: %v", err)
	}
}
