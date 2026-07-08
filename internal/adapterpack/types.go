package adapterpack

import "time"

const (
	ManifestVersion = "hideout.adapter-pack/v1"
	RegistryVersion = "hideout.adapter-pack-registry/v1"
	PlanVersion     = "hideout.adapter-pack-plan/v1"
	TestVersion     = "hideout.adapter-pack-test/v1"

	ManifestFileName = "hideout.adapter-pack.json"

	StateInstalled = "installed"
	StateRevoked   = "revoked"
	StateBuiltin   = "built-in"

	SourceLocal = "local"
	SourceGit   = "git"

	TestPassed  = "passed"
	TestFailed  = "failed"
	TestBlocked = "blocked"

	CapabilityGuestPrivilegePlan = "guest.privilege.plan"
	CapabilityHostFSWritePlan    = "host.fs.write.plan"
)

type Manifest struct {
	SchemaVersion string        `json:"schemaVersion"`
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	Description   string        `json:"description,omitempty"`
	Adapters      []AdapterSpec `json:"adapters"`
	Tests         []TestVector  `json:"tests,omitempty"`
}

type AdapterSpec struct {
	ID                          string   `json:"id"`
	Entrypoint                  string   `json:"entrypoint,omitempty"`
	Script                      string   `json:"script"`
	Commands                    []string `json:"commands"`
	AllowedProposalCapabilities []string `json:"allowedProposalCapabilities,omitempty"`
	Description                 string   `json:"description,omitempty"`
}

type TestVector struct {
	ID        string      `json:"id"`
	AdapterID string      `json:"adapterId"`
	Context   TestContext `json:"context"`
	Expect    TestExpect  `json:"expect"`
}

type TestContext struct {
	Command TestCommand `json:"command"`
}

type TestCommand struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`
	CWD  string   `json:"cwd,omitempty"`
}

type TestExpect struct {
	Outcome        string `json:"outcome"`
	Capability     string `json:"capability,omitempty"`
	ReasonContains string `json:"reasonContains,omitempty"`
}

type SourceSpec struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type SourceLock struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type Revision struct {
	RevisionID       string            `json:"revisionId"`
	Version          string            `json:"version"`
	Source           SourceLock        `json:"source"`
	ManifestDigest   string            `json:"manifestDigest"`
	SourceDigest     string            `json:"sourceDigest"`
	AdapterDigests   map[string]string `json:"adapterDigests,omitempty"`
	TestResultID     string            `json:"testResultId,omitempty"`
	TestStatus       string            `json:"testStatus,omitempty"`
	ValidationStatus string            `json:"validationStatus"`
	InstalledAt      time.Time         `json:"installedAt,omitempty"`
	EvidenceRefs     []string          `json:"evidenceRefs,omitempty"`
}

type RegistryEntry struct {
	PackID           string              `json:"packId"`
	State            string              `json:"state"`
	ActiveRevisionID string              `json:"activeRevisionId,omitempty"`
	Revisions        map[string]Revision `json:"revisions"`
	BuiltIn          bool                `json:"builtIn,omitempty"`
	Description      string              `json:"description,omitempty"`
}

type Registry struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Packs         map[string]RegistryEntry `json:"packs"`
}

type TestResult struct {
	Version    string              `json:"version"`
	ID         string              `json:"id"`
	PackID     string              `json:"packId"`
	RevisionID string              `json:"revisionId"`
	Status     string              `json:"status"`
	Passed     int                 `json:"passed"`
	Failed     int                 `json:"failed"`
	Failures   []string            `json:"failures,omitempty"`
	Results    []TestVectorOutcome `json:"results,omitempty"`
	RecordedAt time.Time           `json:"recordedAt,omitempty"`
}

type TestVectorOutcome struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type RuntimeSource struct {
	Source                      string
	Digest                      string
	Path                        string
	Entrypoint                  string
	Commands                    []string
	AllowedProposalCapabilities []string
	Description                 string
	PackID                      string
	RevisionID                  string
	AdapterID                   string
	LockDigest                  string
}
