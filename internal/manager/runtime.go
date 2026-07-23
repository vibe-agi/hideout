package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
	"github.com/vibe-agi/hideout/internal/runtimeverify"
)

const RuntimeVerifyPlanVersion = "hideout.runtime-verify-plan/v1"

type RuntimeVerifyPlan struct {
	Schema          string                        `json:"schema"`
	EnvironmentID   string                        `json:"environmentId"`
	EnvironmentName string                        `json:"environmentName"`
	Profile         string                        `json:"profile"`
	Backend         string                        `json:"backend"`
	ImageRef        string                        `json:"imageRef"`
	Runtime         environment.RuntimeProvenance `json:"runtime"`
	Effects         []string                      `json:"effects"`
}

type RuntimeVerifyResult struct {
	Schema          string                   `json:"schema"`
	EnvironmentID   string                   `json:"environmentId"`
	EnvironmentName string                   `json:"environmentName"`
	AuditPath       string                   `json:"auditPath,omitempty"`
	ReceiptRef      string                   `json:"receiptRef,omitempty"`
	Status          runtimeverify.StatusView `json:"status"`
}

type RuntimeCatalogView struct {
	Schema         string                  `json:"schema"`
	CatalogRelease string                  `json:"catalogRelease"`
	GeneratedAt    string                  `json:"generatedAt"`
	Families       []runtimecatalog.Family `json:"families"`
}

type RuntimeInspection struct {
	Schema         string                  `json:"schema"`
	CatalogRelease string                  `json:"catalogRelease"`
	Family         runtimecatalog.Family   `json:"family"`
	Revision       runtimecatalog.Revision `json:"revision"`
	Contract       runtimecatalog.Contract `json:"contract"`
	Current        bool                    `json:"current"`
}

func (c Core) RuntimeCatalog() (RuntimeCatalogView, error) {
	catalog, err := c.loadRuntimeCatalog()
	if err != nil {
		return RuntimeCatalogView{}, fmt.Errorf("runtime.catalog.invalid: %w", err)
	}
	return RuntimeCatalogView{
		Schema: catalog.Schema, CatalogRelease: catalog.CatalogRelease,
		GeneratedAt: catalog.GeneratedAt, Families: append([]runtimecatalog.Family(nil), catalog.Families...),
	}, nil
}

func (c Core) InspectRuntime(familyID, revisionID string) (RuntimeInspection, error) {
	catalog, err := c.loadRuntimeCatalog()
	if err != nil {
		return RuntimeInspection{}, fmt.Errorf("runtime.catalog.invalid: %w", err)
	}
	familyID = strings.TrimSpace(familyID)
	revisionID = strings.TrimSpace(revisionID)
	if familyID == "" {
		return RuntimeInspection{}, errors.New("runtime family is required")
	}
	for _, family := range catalog.Families {
		if family.ID != familyID {
			continue
		}
		if revisionID == "" {
			revisionID = family.CurrentRevision
		}
		for _, revision := range family.Revisions {
			if revision.ID == revisionID {
				return RuntimeInspection{
					Schema: catalog.Schema, CatalogRelease: catalog.CatalogRelease,
					Family: family, Revision: revision, Contract: catalog.Contract,
					Current: revision.ID == family.CurrentRevision,
				}, nil
			}
		}
		return RuntimeInspection{}, fmt.Errorf("runtime revision %q is not in family %q", revisionID, familyID)
	}
	return RuntimeInspection{}, fmt.Errorf("runtime family %q is not in the package catalog", familyID)
}

func (c Core) loadRuntimeCatalog() (runtimecatalog.Catalog, error) {
	loader := c.RuntimeCatalogLoader
	if loader == nil {
		loader = runtimecatalog.LoadEmbedded
	}
	return loader()
}

func (c Core) RuntimeStatus(handle string) (runtimeverify.StatusView, error) {
	store, err := c.environmentStore()
	if err != nil {
		return runtimeverify.StatusView{}, err
	}
	record, err := loadByNameOrID(store, strings.TrimSpace(handle))
	if err != nil {
		return runtimeverify.StatusView{}, err
	}
	if record.Runtime != nil {
		if !runtimeReceiptAuthoritySafeForEnvironment(record, c.Store.Root) {
			return runtimeUnknownStatus(record, "workspace overlaps host-only runtime receipt authority"), nil
		}
		if _, err := c.currentRuntimeResolution(record); err != nil {
			return runtimeUnknownStatus(record, "recorded runtime is not current in the package catalog: "+err.Error()), nil
		}
	}
	var receipt *runtimeverify.Receipt
	if loaded, err := (runtimeverify.Store{Root: c.Store.Root}).Load(record.ID); err == nil {
		receipt = &loaded
	} else if !errors.Is(err, os.ErrNotExist) {
		// Malformed or mismatched receipts are ignored by the shared status
		// builder; status remains unknown instead of surfacing stale success.
		receipt = nil
	}
	view := runtimeverify.BuildStatus(record, runtimeEnvironmentRunning(record), receipt)
	if view.Status != runtimeverify.StatusPreviewReady || receipt == nil {
		return view, nil
	}
	inspector := c.RuntimeInstanceInspector
	if inspector == nil {
		inspector = lima.InspectRuntimeInstance
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed, err := inspector(ctx, record.InstanceName, runtimeInstanceExpectation(*record.Runtime))
	if err != nil || !receiptMatchesInstance(*receipt, observed) {
		reason := "current Lima instance does not match the runtime receipt"
		if err != nil {
			reason += ": " + err.Error()
		}
		return runtimeUnknownStatus(record, reason), nil
	}
	return view, nil
}

func (c Core) PlanRuntimeVerify(handle string) (RuntimeVerifyPlan, error) {
	store, err := c.environmentStore()
	if err != nil {
		return RuntimeVerifyPlan{}, err
	}
	record, err := loadByNameOrID(store, strings.TrimSpace(handle))
	if err != nil {
		return RuntimeVerifyPlan{}, err
	}
	if record.Runtime == nil {
		return RuntimeVerifyPlan{}, errors.New("runtime verification requires a catalog-selected environment")
	}
	if record.Backend != "lima" {
		return RuntimeVerifyPlan{}, errors.New("runtime verification requires the Lima backend")
	}
	if record.InstanceName == "" {
		return RuntimeVerifyPlan{}, errors.New("runtime environment has no managed Lima instance identity")
	}
	if record.Status == "running" {
		return RuntimeVerifyPlan{}, errors.New("runtime environment has an active operation; retry after it completes")
	}
	if !runtimeReceiptAuthoritySafeForEnvironment(record, c.Store.Root) {
		return RuntimeVerifyPlan{}, errors.New("runtime verification is unavailable because the workspace overlaps host-only receipt authority")
	}
	if _, err := c.Store.Load(record.Profile); err != nil {
		return RuntimeVerifyPlan{}, fmt.Errorf("load runtime environment profile: %w", err)
	}
	if _, err := c.currentRuntimeResolution(record); err != nil {
		return RuntimeVerifyPlan{}, fmt.Errorf("resolve pinned runtime contract: %w", err)
	}
	return RuntimeVerifyPlan{
		Schema: RuntimeVerifyPlanVersion, EnvironmentID: record.ID,
		EnvironmentName: record.Name, Profile: record.Profile, Backend: record.Backend,
		ImageRef: record.ImageRef, Runtime: *record.Runtime,
		Effects: []string{
			"start or reuse the selected Lima guest",
			"observe declared commands and privilege state",
			"replace the host-only verification receipt",
		},
	}, nil
}

func (c Core) ApplyRuntimeVerify(ctx context.Context, plan RuntimeVerifyPlan, be backend.Backend) (result RuntimeVerifyResult, retErr error) {
	result = RuntimeVerifyResult{Schema: "hideout.runtime-verify-result/v1", EnvironmentID: plan.EnvironmentID, EnvironmentName: plan.EnvironmentName}
	if ctx == nil {
		ctx = context.Background()
	}
	if be == nil {
		return result, errors.New("runtime verification backend is required")
	}
	current, err := c.PlanRuntimeVerify(plan.EnvironmentID)
	if err != nil {
		return result, err
	}
	if !sameRuntimeVerifyPlan(plan, current) {
		return result, errors.New("runtime verification plan no longer matches environment or catalog state")
	}
	if be.Name() != plan.Backend {
		return result, fmt.Errorf("runtime verification backend %q does not match plan backend %q", be.Name(), plan.Backend)
	}
	verifier, ok := be.(backend.RuntimeVerifier)
	if !ok {
		return result, errors.New("selected backend does not implement runtime-only verification")
	}
	envStore, err := c.environmentStore()
	if err != nil {
		return result, err
	}
	lock, err := envStore.Lock(plan.EnvironmentID)
	if err != nil {
		return result, err
	}
	defer func() { retErr = errors.Join(retErr, lock.Unlock()) }()
	record, err := envStore.Load(plan.EnvironmentID)
	if err != nil {
		return result, err
	}
	if record.Runtime == nil || *record.Runtime != plan.Runtime || record.ImageRef != plan.ImageRef {
		return result, errors.New("runtime environment provenance changed after plan")
	}
	if record.Status == environment.StatusCreated {
		if err := c.checkRuntimeDiskProvenance(*record.Runtime); err != nil {
			return result, err
		}
	}
	p, err := c.Store.Load(record.Profile)
	if err != nil {
		return result, err
	}
	runPlan := runtimeVerificationRunPlan(record, p)
	configuration, err := RuntimeConfigurationForProfile(p, record.Backend, record.Mode)
	if err != nil {
		return result, err
	}
	runEnv := selectedRunEnvironment(envStore, record, environment.Spec{
		MachineIdentityID:   configuration.Layers.MachineID,
		BootConfigurationID: configuration.Layers.BootID,
	}, p, true, false, false)
	runSession, err := c.BeginRunSession(runPlan, runEnv, RunSessionOptions{})
	if err != nil {
		return result, err
	}
	defer func() {
		_, closeErr := c.CloseRunSession(runSession)
		retErr = errors.Join(retErr, closeErr)
	}()
	runSession, err = c.OpenRunSessionAudit(runSession, RunAuditOptions{})
	if err != nil {
		return result, err
	}
	result.AuditPath = runSession.AuditPath
	if err := (runtimeverify.Store{Root: c.Store.Root}).Remove(record.ID); err != nil {
		return result, fmt.Errorf("invalidate prior runtime verification: %w", err)
	}
	spec := c.runSpec(runSession, runEnv, RunDataPlane{Env: append([]string(nil), runSession.Env.Env...)}, RunNetwork{})
	if record.Mode == environment.ModeShared {
		// Machine-scoped verification has no project authority. Do not let the
		// generic shared-run transport marker look like a partial attachment.
		spec.Workspace = backend.WorkspaceAttachmentSpec{}
	}
	if err := c.attachRuntimeVerification(&spec, runSession, runEnv, be.Name(), runtimeVerificationMachineAuthority); err != nil {
		return result, err
	}
	session, err := be.Prepare(ctx, spec)
	if err != nil {
		return result, err
	}
	defer func() {
		cleanupErr := be.Cleanup(context.Background(), session)
		if cleanupErr != nil {
			cleanupErr = errors.Join(cleanupErr, (runtimeverify.Store{Root: c.Store.Root}).Remove(record.ID))
		}
		retErr = errors.Join(retErr, cleanupErr)
	}()
	verifyErr := verifier.VerifyRuntime(ctx, session, spec.Env)
	if verifyErr != nil {
		receipt, loadErr := (runtimeverify.Store{Root: c.Store.Root}).Load(record.ID)
		if loadErr == nil && receipt.SessionID == runSession.Layout.ID && receipt.Status == runtimeverify.StatusPreviewFailed {
			record.Status = "ready"
			now := time.Now().UTC()
			record.LastStartedAt = receipt.ObservedAt
			record.LastEndedAt = now
			record.LastSessionID = runSession.Layout.ID
			if saveErr := envStore.Save(record); saveErr != nil {
				return result, errors.Join(verifyErr, saveErr)
			}
			result.ReceiptRef = "environment:" + record.ID + "/runtime-verification"
			result.Status = runtimeverify.BuildStatus(record, true, &receipt)
			return result, verifyErr
		}
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			verifyErr = errors.Join(verifyErr, loadErr)
		}
		if removeErr := (runtimeverify.Store{Root: c.Store.Root}).Remove(record.ID); removeErr != nil {
			verifyErr = errors.Join(verifyErr, removeErr)
		}
		result.Status = runtimeverify.BuildStatus(record, runtimeEnvironmentRunning(record), nil)
		return result, verifyErr
	}
	receipt, receiptErr := (runtimeverify.Store{Root: c.Store.Root}).Load(record.ID)
	if receiptErr == nil {
		record.Status = "ready"
		now := time.Now().UTC()
		record.LastStartedAt = receipt.ObservedAt
		record.LastEndedAt = now
		record.LastSessionID = runSession.Layout.ID
		if err := envStore.Save(record); err != nil {
			return result, errors.Join(verifyErr, err)
		}
		result.ReceiptRef = "environment:" + record.ID + "/runtime-verification"
		result.Status = runtimeverify.BuildStatus(record, true, &receipt)
	} else {
		result.Status = runtimeverify.BuildStatus(record, runtimeEnvironmentRunning(record), nil)
	}
	return result, verifyErr
}

func (c Core) currentRuntimeResolution(record environment.Record) (runtimecatalog.Resolution, error) {
	if record.Runtime == nil {
		return runtimecatalog.Resolution{}, errors.New("environment has no runtime provenance")
	}
	catalog, err := c.loadRuntimeCatalog()
	if err != nil {
		return runtimecatalog.Resolution{}, err
	}
	resolution, err := catalog.Resolve(runtimecatalog.Selection{
		Family: record.Runtime.Family, Revision: record.Runtime.Revision,
		HostOS: record.Runtime.HostOS, HostArch: record.Runtime.HostArch,
	})
	if err != nil {
		return runtimecatalog.Resolution{}, err
	}
	if resolution.Provenance != *record.Runtime || resolution.ImageRef != record.ImageRef {
		return runtimecatalog.Resolution{}, errors.New("recorded runtime provenance differs from the current catalog")
	}
	return resolution, nil
}

func runtimeInstanceExpectation(provenance environment.RuntimeProvenance) backend.RuntimeInstanceExpectation {
	return backend.RuntimeInstanceExpectation{
		ImageLocation: provenance.ArtifactLocation, ImageSHA256: provenance.ArtifactSHA256,
		PackageInventorySHA256: strings.TrimPrefix(provenance.PackageInventoryDigest, "sha256:"),
		HostOS:                 provenance.HostOS, HostArch: provenance.HostArch, GuestArch: provenance.GuestArch,
		VMType: "vz",
	}
}

func runtimeUnknownStatus(record environment.Record, reason string) runtimeverify.StatusView {
	view := runtimeverify.StatusView{Status: runtimeverify.StatusUnknown, Running: runtimeEnvironmentRunning(record), Reason: reason}
	if record.Runtime != nil {
		view.Family = record.Runtime.Family
		view.Revision = record.Runtime.Revision
		view.Maturity = record.Runtime.Maturity
		view.ArtifactSHA256 = record.Runtime.ArtifactSHA256
	}
	return view
}

func receiptMatchesInstance(receipt runtimeverify.Receipt, observed backend.RuntimeInstanceObservation) bool {
	return receipt.Instance.Name == observed.InstanceName && receipt.Instance.Status == observed.Status &&
		receipt.Instance.VMType == observed.VMType && receipt.Instance.HostOS == observed.HostOS &&
		receipt.Instance.HostArch == observed.HostArch && receipt.Instance.GuestArch == observed.GuestArch &&
		receipt.Instance.ImageLocation == observed.ImageLocation && receipt.Instance.ImageSHA256 == observed.ImageSHA256 &&
		receipt.Instance.ActiveBuildIdentity == receipt.Provenance.PackageInventoryDigest &&
		receipt.Instance.ActiveBuildIdentity == "sha256:"+observed.PackageInventorySHA256 &&
		receipt.Instance.BootID == observed.BootID
}

func runtimeReceiptAuthoritySafe(workspace, storeRoot string) bool {
	workspacePath, err := canonicalExistingWorkspace(workspace)
	if err != nil {
		return false
	}
	rootPath := canonicalPathBestEffort(storeRoot)
	return rootPath != "" && !pathInRoot(workspacePath, rootPath) && !pathInRoot(rootPath, workspacePath)
}

func runtimeReceiptAuthoritySafeForEnvironment(record environment.Record, storeRoot string) bool {
	binding, ok := pinnedEnvironmentWorkspace(record)
	if !ok {
		return record.Mode == environment.ModeShared
	}
	return runtimeReceiptAuthoritySafe(binding.HostRoot, storeRoot)
}

func runtimeVerificationRunPlan(record environment.Record, p profile.Profile) RunPlan {
	binding, _ := pinnedEnvironmentWorkspace(record)
	return RunPlan{
		Version: RunPlanVersion, ProfileName: p.Name, Backend: record.Backend,
		Workspace: binding.HostRoot, GuestWorkspace: binding.GuestRoot,
		WorkspaceMode: p.Workspace.Mode, PathMode: p.Workspace.PathMode,
		NetworkMode: p.Network.Mode, Profile: p, RuntimeProfile: p,
	}
}

func runtimeEnvironmentRunning(record environment.Record) bool {
	return record.Status == "ready" || record.Status == "running"
}

func sameRuntimeVerifyPlan(left, right RuntimeVerifyPlan) bool {
	return left.Schema == right.Schema && left.EnvironmentID == right.EnvironmentID &&
		left.EnvironmentName == right.EnvironmentName && left.Profile == right.Profile &&
		left.Backend == right.Backend && left.ImageRef == right.ImageRef &&
		left.Runtime == right.Runtime && slices.Equal(left.Effects, right.Effects)
}
