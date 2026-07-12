package productevidence

const (
	Feature021 = "021-ui-e2e-proof"
	Feature022 = "022-alpha-first-run-e2e"
	Feature023 = "023-hostfs-decision-e2e"
	Feature024 = "024-doctor-package-recovery-e2e"
	Feature025 = "025-documentation-truth-gate"
	Feature029 = "029-hostfs-discoverable-namespace"
	Feature030 = "030-host-capability-projection"

	Proof021EvidenceSchema        = "021.evidence.schema"
	Proof021EvidenceRedaction     = "021.evidence.redaction"
	Proof021DocsBoundary          = "021.docs.boundary"
	Proof021WebUIBrowserConsole   = "021.webui.browser.console"
	Proof021WebUIBrowserLive      = "021.webui.browser.live-update"
	Proof021WebUIBrowserNoticeAck = "021.webui.browser.notice-ack"
	Proof021WebUIBrowserAuth      = "021.webui.browser.auth-refusal"
	Proof021TUIPTYConsole         = "021.tui.pty.console"
	Proof021TUIPTYLive            = "021.tui.pty.live-update"
	Proof021TUIPTYNoPolling       = "021.tui.pty.no-interval-polling"
	Proof021TUIPTYFallback        = "021.tui.pty.fallback"

	Proof022LocalFastInstall       = "022.first-run.local-fast.install"
	Proof022LocalFastVerify        = "022.first-run.local-fast.verify"
	Proof022LocalFastInit          = "022.first-run.local-fast.init"
	Proof022LocalFastRun           = "022.first-run.local-fast.run"
	Proof022LocalFastAuditBoundary = "022.first-run.local-fast.audit-boundary"
	Proof022DocsOrder              = "022.first-run.docs.order"
	Proof022FailureFixtures        = "022.first-run.failure.fixtures"
	Proof022RealBackend            = "022.first-run.real-backend"
	Proof022RealBackendNotRun      = "022.first-run.real-backend.not-run"

	Proof023LocalFastLifecycle  = "023.hostfs-decision.local-fast.lifecycle"
	Proof023LocalFastClaimRace  = "023.hostfs-decision.local-fast.claim-race"
	Proof023LocalFastTimeout    = "023.hostfs-decision.local-fast.timeout"
	Proof023LocalFastVisibility = "023.hostfs-decision.local-fast.visibility"
	Proof023LocalFastRedaction  = "023.hostfs-decision.local-fast.redaction"
	Proof023RealGate2Lifecycle  = "023.hostfs-decision.real-gate2.lifecycle"
	Proof023RealGate2NotRun     = "023.hostfs-decision.real-gate2.not-run"

	Proof024PackageRepairLoop = "024.recovery.package.repair-loop"
	Proof024DoctorSafeFixLoop = "024.recovery.doctor.safe-fix-loop"
	Proof024DoctorGuidance    = "024.recovery.doctor.guidance-only"
	Proof024Redaction         = "024.recovery.redaction"

	Proof025ClaimBoundaries = "025.docs.claim-boundaries"
	Proof025OverclaimScan   = "025.docs.overclaim-scan"
	Proof025CommandExamples = "025.docs.command-examples"
	Proof025CrossDoc        = "025.docs.cross-doc-consistency"

	Proof029UnitPolicy             = "029.hostfs-visibility.unit.policy"
	Proof029UnitTypedErrno         = "029.hostfs-visibility.unit.typed-errno"
	Proof029LocalDecisionLifecycle = "029.hostfs-visibility.local-fast.decision-lifecycle"
	Proof029LocalRedaction         = "029.hostfs-visibility.local-fast.redaction"
	Proof029RealGate2Namespace     = "029.hostfs-visibility.real-gate2.namespace"
	Proof029RealGate2LiveGrant     = "029.hostfs-visibility.real-gate2.live-grant"
	Proof029RealGate2NotRun        = "029.hostfs-visibility.real-gate2.not-run"
	Proof029DocsClaimBoundary      = "029.hostfs-visibility.docs.claim-boundary"

	Proof030Gate0Mechanics           = "030.projection.gate0.mechanics"
	Proof030RealGate2CodeOpen        = "030.projection.real-gate2.code-open"
	Proof030RealGate2PrivacyChannels = "030.projection.real-gate2.privacy-three-channel"
	Proof030RealGate2TrustedGrant    = "030.projection.real-gate2.trusted-grant"
	Proof030RealGate2NotRun          = "030.projection.real-gate2.not-run"
	Proof030DocsClaimBoundary        = "030.projection.docs.claim-boundary"
)

var Claim021EvidenceSchema = CoveredClaim{
	ClaimID:     "021.FR-011",
	Source:      "spec",
	Description: "UI proof writes a product-hardening evidence manifest",
	Scope:       "evidence",
}

var Claim021BrowserConsole = CoveredClaim{
	ClaimID:     "021.FR-001",
	Source:      "spec",
	Description: "WebUI opens in a real local browser context",
	Scope:       "browser",
}

var Claim021BrowserNotRun = CoveredClaim{
	ClaimID:     "021.FR-013",
	Source:      "spec",
	Description: "Missing browser prerequisites record not-run evidence",
	Scope:       "browser",
}

var Claim021TUINotRun = CoveredClaim{
	ClaimID:     "021.FR-013",
	Source:      "spec",
	Description: "Missing terminal prerequisites record not-run evidence",
	Scope:       "tui",
}

var Claim022LocalFast = CoveredClaim{
	ClaimID:     "022.SC-001",
	Source:      "spec",
	Description: "Local-fast first-run installs from package and runs one command",
	Scope:       "first-run",
}

var Claim022SingleInit = CoveredClaim{
	ClaimID:     "022.FR-005",
	Source:      "spec",
	Description: "Selected first-run profile is initialized exactly once",
	Scope:       "profile",
}

var Claim022AuditBoundary = CoveredClaim{
	ClaimID:     "022.FR-006",
	Source:      "spec",
	Description: "First command captures audit and Boundary evidence",
	Scope:       "evidence",
}

var Claim022DocsOrder = CoveredClaim{
	ClaimID:     "022.FR-013",
	Source:      "spec",
	Description: "First-run docs use skip-init before explicit init",
	Scope:       "docs",
}

var Claim022FailureFixtures = CoveredClaim{
	ClaimID:     "022.SC-004",
	Source:      "spec",
	Description: "Failure fixtures do not produce passing first-run evidence",
	Scope:       "fail-closed",
}

var Claim022RealBackend = CoveredClaim{
	ClaimID:     "022.FR-008",
	Source:      "spec",
	Description: "Real backend proof passes only through the real backend path",
	Scope:       "backend",
}

var Claim023Lifecycle = CoveredClaim{
	ClaimID:     "023.FR-001",
	Source:      "spec",
	Description: "HostFS write E2E records mode and operation coverage",
	Scope:       "hostfs",
}

var Claim023GuestStagedRead = CoveredClaim{
	ClaimID:     "023.FR-002",
	Source:      "spec",
	Description: "Target reads reflect staged overlay state before apply",
	Scope:       "hostfs",
}

var Claim023HostLowerBeforeApply = CoveredClaim{
	ClaimID:     "023.FR-003",
	Source:      "spec",
	Description: "Host lower remains unchanged before operator apply",
	Scope:       "hostfs",
}

var Claim023Apply = CoveredClaim{
	ClaimID:     "023.FR-004",
	Source:      "spec",
	Description: "Operator apply mutates only planned host state",
	Scope:       "hostfs",
}

var Claim023Conflict = CoveredClaim{
	ClaimID:     "023.FR-005",
	Source:      "spec",
	Description: "Stale or conflicting apply fails closed",
	Scope:       "hostfs",
}

var Claim023Visibility = CoveredClaim{
	ClaimID:     "023.FR-006",
	Source:      "spec",
	Description: "Decisions are visible without private tokens or provider refs",
	Scope:       "visibility",
}

var Claim023ClaimRace = CoveredClaim{
	ClaimID:     "023.FR-007",
	Source:      "spec",
	Description: "Exactly one decision claimant wins",
	Scope:       "decision",
}

var Claim023DecisionOutcomes = CoveredClaim{
	ClaimID:     "023.FR-008",
	Source:      "spec",
	Description: "Approve, deny, and timeout outcomes update decision status",
	Scope:       "decision",
}

var Claim023RealBoundary = CoveredClaim{
	ClaimID:     "023.FR-010",
	Source:      "spec",
	Description: "Local-fast evidence does not satisfy real HostFS claims",
	Scope:       "backend",
}

var Claim023Redaction = CoveredClaim{
	ClaimID:     "023.FR-011",
	Source:      "spec",
	Description: "Public HostFS and decision artifacts omit control-plane material",
	Scope:       "redaction",
}

var Claim023Coverage = CoveredClaim{
	ClaimID:     "023.SC-007",
	Source:      "spec",
	Description: "Evidence lists covered and uncovered HostFS write classes",
	Scope:       "coverage",
}

var Claim024PackageRepair = CoveredClaim{
	ClaimID:     "024.FR-002",
	Source:      "spec",
	Description: "Package verify detects obsolete package-owned leftovers and repair fixes them",
	Scope:       "package",
}

var Claim024PackagePreservation = CoveredClaim{
	ClaimID:     "024.FR-005",
	Source:      "spec",
	Description: "Package repair preserves durable store state and unrelated files",
	Scope:       "package",
}

var Claim024DoctorSafeFix = CoveredClaim{
	ClaimID:     "024.FR-008",
	Source:      "spec",
	Description: "Doctor fix apply performs only typed safe repairs",
	Scope:       "doctor",
}

var Claim024DoctorGuidance = CoveredClaim{
	ClaimID:     "024.FR-006",
	Source:      "spec",
	Description: "Doctor deep emits observed facts, next actions, and gate-required markers",
	Scope:       "doctor",
}

var Claim024DoctorExport = CoveredClaim{
	ClaimID:     "024.FR-010",
	Source:      "spec",
	Description: "Selected doctor report export validates through the export schema",
	Scope:       "export",
}

var Claim024Redaction = CoveredClaim{
	ClaimID:     "024.FR-011",
	Source:      "spec",
	Description: "Public recovery artifacts omit control-plane material",
	Scope:       "redaction",
}

var Claim024LocalOnly = CoveredClaim{
	ClaimID:     "024.FR-012",
	Source:      "spec",
	Description: "Local recovery evidence is not release readiness or real gate proof",
	Scope:       "boundary",
}

var Claim025ClaimBoundaries = CoveredClaim{
	ClaimID:     "025.FR-001",
	Source:      "spec",
	Description: "Current product claims map to proof ids, gates, or non-claims",
	Scope:       "docs",
}

var Claim025OverclaimScan = CoveredClaim{
	ClaimID:     "025.FR-002",
	Source:      "spec",
	Description: "Known overclaim patterns are rejected with file and line",
	Scope:       "docs",
}

var Claim025CommandExamples = CoveredClaim{
	ClaimID:     "025.FR-005",
	Source:      "spec",
	Description: "Curated command examples are recognized or explicitly non-executed",
	Scope:       "commands",
}

var Claim025CrossDoc = CoveredClaim{
	ClaimID:     "025.FR-009",
	Source:      "spec",
	Description: "README, STATUS, test plan, and Gate 0 agree on product-hardening scripts",
	Scope:       "docs",
}
