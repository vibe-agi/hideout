package productevidence

import (
	"errors"
	"fmt"
)

const (
	Feature042 = "042-disposable-orphan-recovery"

	Proof042Gate0Mechanics    = "042.disposable-recovery.gate0.mechanics"
	Proof042Gate0Model        = "042.disposable-recovery.gate0.model"
	Proof042RealRecovery      = "042.disposable-recovery.real-gate2.recovery"
	Proof042RealGate2NotRun   = "042.disposable-recovery.real-gate2.not-run"
	Proof042DocsClaimBoundary = "042.disposable-recovery.docs.claim-boundary"
)

var requiredDisposableRecoveryChecks = []string{
	"boundedWorkers",
	"crashAfterBackendCleanup",
	"crashAfterIntent",
	"crashAfterJournalRemoval",
	"crashAfterStableAbsence",
	"ephemeralIdentity",
	"exactInstance",
	"gatewayCleared",
	"historicalJournalRefused",
	"identityMismatchRefused",
	"journalCleared",
	"liveOwnerRefused",
	"nameOnlyRefused",
	"nonDisposableRefused",
	"ordinaryFinalizer",
	"recordCleared",
	"runtimeCleared",
	"shutdownInterrupted",
	"stableAbsenceTwice",
	"startupStatusAvailable",
	"statusOnlyRefused",
	"targetFailure",
	"unprovableOwnerRefused",
	"zeroUnauthorizedCleanupCalls",
}

type disposableRecoveryEvidence struct {
	Schema    string          `json:"schema"`
	Status    string          `json:"status"`
	Commit    string          `json:"commit"`
	Dirty     bool            `json:"dirty"`
	Backend   string          `json:"backend"`
	HostOS    string          `json:"hostOS"`
	HostArch  string          `json:"hostArch"`
	GuestArch string          `json:"guestArch"`
	Checks    map[string]bool `json:"checks"`
	Samples   struct {
		OrdinaryRuns       int `json:"ordinaryRuns"`
		CrashSchedules     int `json:"crashSchedules"`
		RestartCheckpoints int `json:"restartCheckpoints"`
	} `json:"samples"`
	Timing struct {
		StartupStatusP95MS float64 `json:"startupStatusP95Ms"`
		RecoveryP95MS      float64 `json:"recoveryP95Ms"`
		RecoveryTimeouts   int     `json:"recoveryTimeouts"`
	} `json:"timing"`
	DestructiveCalls struct {
		Authorized   int `json:"authorized"`
		Unauthorized int `json:"unauthorized"`
	} `json:"destructiveCalls"`
	Residue struct {
		EnvironmentRecords int `json:"environmentRecords"`
		LifecycleJournals  int `json:"lifecycleJournals"`
		BackendInstances   int `json:"backendInstances"`
		Gateways           int `json:"gateways"`
		RuntimeReceipts    int `json:"runtimeReceipts"`
		OwnerRecords       int `json:"ownerRecords"`
	} `json:"residue"`
	NonClaims struct {
		HistoricalJournalOnly string `json:"historicalJournalOnly"`
		OrdinaryOrphans       string `json:"ordinaryOrphans"`
	} `json:"nonClaims"`
}

func validateDisposableRecoveryArtifact(data []byte, expectedCommit string) error {
	var evidence disposableRecoveryEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("disposable recovery evidence: %w", err)
	}
	if evidence.Schema != "hideout.disposable-recovery-gate2/v1" ||
		evidence.Status != StatusPassed || evidence.Backend != "lima" ||
		evidence.HostOS != "darwin" || evidence.HostArch != "arm64" ||
		evidence.GuestArch != "aarch64" {
		return errors.New("disposable recovery evidence identity or platform is invalid")
	}
	if !IsCanonicalCommit(evidence.Commit) ||
		(expectedCommit != "" && evidence.Commit != expectedCommit) || evidence.Dirty {
		return errors.New("disposable recovery evidence is not bound to the clean candidate commit")
	}
	if len(evidence.Checks) != len(requiredDisposableRecoveryChecks) {
		return errors.New("disposable recovery evidence check inventory drifted")
	}
	for _, check := range requiredDisposableRecoveryChecks {
		if !evidence.Checks[check] {
			return fmt.Errorf("disposable recovery evidence check %q did not pass", check)
		}
	}
	if evidence.Samples.OrdinaryRuns < 30 {
		return errors.New("disposable recovery evidence has fewer than 30 ordinary runs")
	}
	if evidence.Samples.CrashSchedules < 100 {
		return errors.New("disposable recovery evidence has fewer than 100 crash schedules")
	}
	if evidence.Samples.RestartCheckpoints < 4 {
		return errors.New("disposable recovery evidence is missing restart checkpoints")
	}
	if evidence.Timing.RecoveryTimeouts != 0 {
		return errors.New("disposable recovery evidence contains a recovery timeout")
	}
	if evidence.Timing.StartupStatusP95MS <= 0 || evidence.Timing.StartupStatusP95MS > 1000 ||
		evidence.Timing.RecoveryP95MS <= 0 || evidence.Timing.RecoveryP95MS > 30_000 {
		return errors.New("disposable recovery timing bounds were not met")
	}
	if evidence.DestructiveCalls.Authorized <= 0 || evidence.DestructiveCalls.Unauthorized != 0 {
		return errors.New("disposable recovery evidence recorded unauthorized destructive calls")
	}
	if evidence.Residue.EnvironmentRecords != 0 || evidence.Residue.LifecycleJournals != 0 ||
		evidence.Residue.BackendInstances != 0 || evidence.Residue.Gateways != 0 ||
		evidence.Residue.RuntimeReceipts != 0 || evidence.Residue.OwnerRecords != 0 {
		return errors.New("disposable recovery evidence retained cleanup residue")
	}
	if evidence.NonClaims.HistoricalJournalOnly != "not-auto-recovered" ||
		evidence.NonClaims.OrdinaryOrphans != "report-only" {
		return errors.New("disposable recovery evidence overclaims generic orphan recovery")
	}
	return nil
}
