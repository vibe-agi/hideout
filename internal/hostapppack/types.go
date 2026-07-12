package hostapppack

import "time"

import "github.com/vibe-agi/hideout/internal/packsnapshot"

const (
	ManifestVersion   = "hideout.host-app-pack/v1"
	RegistryVersion   = "hideout.host-app-pack-registry/v1"
	EnablementVersion = "hideout.host-app-enablement/v1"
	InspectionVersion = "hideout.host-app-inspection/v1"
	TestResultVersion = "hideout.host-app-pack-test/v1"

	ManifestFileName = "hideout.host-app-pack.json"
	SourceLocal      = packsnapshot.SourceLocal
	SourceGit        = packsnapshot.SourceGit

	PlatformDarwin = "darwin"

	CapabilityOpenResource = "host.app.open-resource"
	ResourceWorkspace      = "workspace"
	ResourceHostFSPortal   = "hostfs-portal"
	ResultNone             = "none"
	AccessSafe             = "safe"
	AccessAskEachRun       = "ask-each-run"
	GrammarOpenResourceV1  = "open-resource-v1"
	UnknownFlagsDeny       = "deny"

	PackInstalled = "installed"
	PackRevoked   = "revoked"
	PackRemoved   = "removed"

	RevisionInstalled = "installed"
	RevisionRevoked   = "revoked"

	ValidationPassed = "passed"
	ValidationFailed = "failed"
	TestNotRun       = "not-run"
	TestPassed       = "passed"
	TestFailed       = "failed"

	EnablementEnabled   = "enabled"
	EnablementSuspended = "suspended"
	EnablementDisabled  = "disabled"
	EnablementRevoked   = "revoked"

	MaxApps               = 16
	MaxBindings           = 32
	MaxCommandsPerBinding = 16
	MaxCommandsPerProfile = 64
	MaxTests              = 64
	MaxDescriptionBytes   = 512
	MaxHintBytes          = 512
	MaxURLBytes           = 2048
	MaxSlugBytes          = 64
	MaxPackIDBytes        = 96
	MaxStorageIDBytes     = 128
	MaxVersionBytes       = 128
	MaxPathBytes          = 1024
	MaxExecutableBytes    = 256
	MaxBundleNameBytes    = 128
	MaxFlagBytes          = 64
	MaxGrammarFlags       = 8
	MaxBundleNames        = 8
	MaxArgv               = 32
	MaxArgBytes           = 1024
)

type SourceSpec struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type SourceLock struct {
	Kind       string    `json:"kind"`
	LocalPath  string    `json:"localPath,omitempty"`
	URL        string    `json:"url,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	AcquiredAt time.Time `json:"acquiredAt"`
}

type Manifest struct {
	SchemaVersion string        `json:"schemaVersion"`
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	Description   string        `json:"description"`
	Apps          []AppSpec     `json:"apps"`
	Bindings      []BindingSpec `json:"bindings"`
	Tests         []TestVector  `json:"tests"`
	InstallHint   *InstallHint  `json:"installHint,omitempty"`
}

type InstallHint struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}

type AppSpec struct {
	ID                     string     `json:"id"`
	Platforms              []string   `json:"platforms"`
	BundleNames            []string   `json:"bundleNames"`
	ExecutableRelativePath string     `json:"executableRelativePath"`
	ExpectedBundleID       string     `json:"expectedBundleId,omitempty"`
	ExpectedTeamID         string     `json:"expectedTeamId,omitempty"`
	RequestedSafetyProfile string     `json:"requestedSafetyProfile,omitempty"`
	Launch                 LaunchSpec `json:"launch"`
}

type LaunchSpec struct {
	GotoFlag        string `json:"gotoFlag,omitempty"`
	NewWindowFlag   string `json:"newWindowFlag,omitempty"`
	ReuseWindowFlag string `json:"reuseWindowFlag,omitempty"`
	GotoSeparator   string `json:"gotoSeparator,omitempty"`
}

type BindingSpec struct {
	ID              string      `json:"id"`
	Commands        []string    `json:"commands"`
	AppID           string      `json:"appId"`
	CapabilityID    string      `json:"capabilityId"`
	ResourceKinds   []string    `json:"resourceKinds"`
	ResultPolicy    string      `json:"resultPolicy"`
	RequestedAccess string      `json:"requestedAccess"`
	Grammar         GrammarSpec `json:"grammar"`
}

type GrammarSpec struct {
	Kind             string   `json:"kind"`
	ResourceCount    int      `json:"resourceCount"`
	GotoFlags        []string `json:"gotoFlags"`
	NewWindowFlags   []string `json:"newWindowFlags"`
	ReuseWindowFlags []string `json:"reuseWindowFlags"`
	UnknownFlags     string   `json:"unknownFlags"`
}

type TestVector struct {
	ID        string          `json:"id"`
	BindingID string          `json:"bindingId"`
	Argv      []string        `json:"argv"`
	Expected  TestExpectation `json:"expected"`
}

type TestExpectation struct {
	Resource   string `json:"resource"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	WindowMode string `json:"windowMode"`
}

type Revision struct {
	RevisionID                string     `json:"revisionId"`
	PackID                    string     `json:"packId"`
	Source                    SourceLock `json:"source"`
	SourceDigest              string     `json:"sourceDigest"`
	ManifestDigest            string     `json:"manifestDigest"`
	BasePermissionFingerprint string     `json:"basePermissionFingerprint"`
	ValidationStatus          string     `json:"validationStatus"`
	TestStatus                string     `json:"testStatus"`
	InstalledAt               time.Time  `json:"installedAt"`
	State                     string     `json:"state"`
}

type RegistryEntry struct {
	ID               string     `json:"id"`
	State            string     `json:"state"`
	ActiveRevisionID string     `json:"activeRevisionId,omitempty"`
	Revisions        []Revision `json:"revisions"`
}

type Registry struct {
	Schema    string          `json:"schema"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Packs     []RegistryEntry `json:"packs"`
}

type TestResult struct {
	SchemaVersion string        `json:"schemaVersion"`
	ID            string        `json:"id"`
	PackID        string        `json:"packId"`
	RevisionID    string        `json:"revisionId"`
	Status        string        `json:"status"`
	Passed        int           `json:"passed"`
	Failed        int           `json:"failed"`
	Failures      []string      `json:"failures,omitempty"`
	Results       []TestOutcome `json:"results,omitempty"`
	RecordedAt    time.Time     `json:"recordedAt"`
}

type TestOutcome struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type Enablement struct {
	Schema                    string               `json:"schema"`
	Profile                   string               `json:"profile"`
	PackID                    string               `json:"packId"`
	RevisionID                string               `json:"revisionId"`
	BindingIDs                []string             `json:"bindingIds"`
	SourceDigest              string               `json:"sourceDigest"`
	BasePermissionFingerprint string               `json:"basePermissionFingerprint"`
	PermissionFingerprint     string               `json:"permissionFingerprint"`
	Access                    string               `json:"access"`
	ObservedIdentityDigest    string               `json:"observedIdentityDigest"`
	UnverifiedAppTrust        []UnverifiedAppTrust `json:"unverifiedAppTrust,omitempty"`
	ConflictReplacements      map[string]string    `json:"conflictReplacements"`
	EnabledAt                 time.Time            `json:"enabledAt"`
	State                     string               `json:"state"`
	Reason                    string               `json:"reason"`
}

type Inspection struct {
	Schema      string            `json:"schema"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Entries     []InspectionEntry `json:"entries"`
}

type InspectionEntry struct {
	Summary     InspectionSummary     `json:"summary"`
	Package     InspectionPackage     `json:"package"`
	Permissions InspectionPermissions `json:"permissions"`
	AppIdentity InspectionAppIdentity `json:"appIdentity"`
	Binding     InspectionBinding     `json:"binding"`
	Safety      InspectionSafety      `json:"safety"`
	Runtime     InspectionRuntime     `json:"runtime"`
	Hint        *InspectionHint       `json:"hint,omitempty"`
}

type InspectionSummary struct {
	Command    string `json:"command"`
	App        string `json:"app"`
	Profile    string `json:"profile"`
	Access     string `json:"access"`
	Readiness  string `json:"readiness"`
	NextAction string `json:"nextAction,omitempty"`
}

type InspectionPackage struct {
	ID           string `json:"id"`
	RevisionID   string `json:"revisionId"`
	SourceKind   string `json:"sourceKind"`
	SourceDigest string `json:"sourceDigest"`
	TestStatus   string `json:"testStatus"`
}

type InspectionPermissions struct {
	Fingerprint string   `json:"fingerprint"`
	Status      string   `json:"status"`
	Diff        []string `json:"diff"`
}

type InspectionAppIdentity struct {
	Verification  string `json:"verification"`
	RootClass     string `json:"rootClass"`
	OwnerClass    string `json:"ownerClass"`
	BundleID      string `json:"bundleId,omitempty"`
	TeamID        string `json:"teamId,omitempty"`
	CodeIdentity  string `json:"codeIdentity,omitempty"`
	ContentDigest string `json:"contentDigest,omitempty"`
}

type InspectionBinding struct {
	ID            string   `json:"id"`
	Commands      []string `json:"commands"`
	ResourceKinds []string `json:"resourceKinds"`
	CapabilityID  string   `json:"capabilityId"`
	Grammar       string   `json:"grammar"`
	ResultPolicy  string   `json:"resultPolicy"`
	ShadowStatus  string   `json:"shadowStatus"`
}

type InspectionSafety struct {
	RequestedProfile  string `json:"requestedProfile,omitempty"`
	CompatibleProfile string `json:"compatibleProfile,omitempty"`
	Posture           string `json:"posture"`
}

type InspectionRuntime struct {
	ActiveInCurrentRun bool   `json:"activeInCurrentRun"`
	GrantState         string `json:"grantState"`
	LastOutcome        string `json:"lastOutcome,omitempty"`
	AuditRef           string `json:"auditRef,omitempty"`
}

type InspectionHint struct {
	Untrusted bool   `json:"untrusted"`
	Text      string `json:"text"`
	URL       string `json:"url,omitempty"`
}

type InstallRequest struct {
	Source                            SourceSpec
	ExpectedSourceDigest              string
	ExpectedBasePermissionFingerprint string
}

type EffectivePermissionContext struct {
	Access               string
	SafetyProfileID      string
	SafetyProfileVersion string
	BindingIDs           []string
	ConflictReplacements map[string]string
}
