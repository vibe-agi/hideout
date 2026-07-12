package productevidence

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/environment"
)

var canonicalCommitRE = regexp.MustCompile(`^[a-f0-9]{12,40}$`)

const (
	Feature031           = "031-supported-cli-runtime"
	RuntimeBindingSchema = "hideout.runtime-evidence-binding/v1"

	Proof031Gate0Mechanics     = "031.runtime.gate0.mechanics"
	Proof031RealImage          = "031.runtime.real-image"
	Proof031Baseline           = "031.runtime.baseline"
	Proof031AgentInstall       = "031.runtime.agent-install"
	Proof031AgentPrivacy       = "031.runtime.agent-privacy"
	Proof031ReadinessParity    = "031.runtime.readiness-parity"
	Proof031BoundaryRegression = "031.runtime.boundary-regression"
	Proof031DocsClaimBoundary  = "031.runtime.docs.claim-boundary"
)

// RuntimeBinding ties a proof to one real guest artifact and candidate source
// state. It is typed evidence, not a free-form note, so freshness identity
// cannot be overridden by producer text.
type RuntimeBinding struct {
	Schema          string `json:"schema"`
	Family          string `json:"family"`
	Revision        string `json:"revision"`
	ArtifactSHA256  string `json:"artifactSHA256"`
	EnvironmentID   string `json:"environmentId"`
	HostOS          string `json:"hostOS"`
	HostArch        string `json:"hostArch"`
	GuestArch       string `json:"guestArch"`
	CandidateCommit string `json:"candidateCommit"`
	CandidateDirty  bool   `json:"candidateDirty"`
}

// RuntimeExpectation is anchored in the packaged catalog and current source
// candidate. EnvironmentID is intentionally absent because disposable real
// gates have independent environment identities.
type RuntimeExpectation struct {
	Family          string
	Revision        string
	ArtifactSHA256  string
	HostOS          string
	HostArch        string
	GuestArch       string
	CandidateCommit string
	RequireClean    bool
}

func (e RuntimeExpectation) Validate() error {
	for name, value := range map[string]string{
		"family": e.Family, "revision": e.Revision, "hostOS": e.HostOS,
		"hostArch": e.HostArch, "guestArch": e.GuestArch, "candidateCommit": e.CandidateCommit,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 128 || audit.RedactString(value) != value {
			return fmt.Errorf("runtime expectation %s is required, bounded, and control-plane clean", name)
		}
	}
	if !isLowerHexSHA256(e.ArtifactSHA256) {
		return errors.New("runtime expectation artifactSHA256 must be 64 lowercase hex characters")
	}
	if !IsCanonicalCommit(e.CandidateCommit) {
		return errors.New("runtime expectation candidateCommit must be a canonical 12-40 character lowercase hex commit")
	}
	if !e.RequireClean {
		return errors.New("runtime expectation must require a clean candidate source state")
	}
	return nil
}

func (b RuntimeBinding) Validate() error {
	if b.Schema != RuntimeBindingSchema {
		return fmt.Errorf("unsupported runtime evidence schema %q", b.Schema)
	}
	for name, value := range map[string]string{
		"family": b.Family, "revision": b.Revision, "hostOS": b.HostOS,
		"hostArch": b.HostArch, "guestArch": b.GuestArch, "candidateCommit": b.CandidateCommit,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 128 || audit.RedactString(value) != value {
			return fmt.Errorf("runtime evidence %s is required, bounded, and control-plane clean", name)
		}
	}
	if !isLowerHexSHA256(b.ArtifactSHA256) {
		return errors.New("runtime evidence artifactSHA256 must be 64 lowercase hex characters")
	}
	if !IsCanonicalCommit(b.CandidateCommit) {
		return errors.New("runtime evidence candidateCommit must be a canonical 12-40 character lowercase hex commit")
	}
	if !environment.ValidID(b.EnvironmentID) {
		return fmt.Errorf("runtime evidence environmentId %q is invalid", b.EnvironmentID)
	}
	return nil
}

func IsCanonicalCommit(value string) bool {
	return canonicalCommitRE.MatchString(value)
}

func (p PackageIdentity) ValidateCandidateCommit(candidateCommit string) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("package identity name is required")
	}
	if !IsCanonicalCommit(p.Version) {
		return errors.New("package identity version must be the canonical candidate commit")
	}
	if !IsCanonicalCommit(candidateCommit) {
		return errors.New("candidate commit is not canonical")
	}
	if p.Version != candidateCommit {
		return fmt.Errorf("package candidate commit %q does not match runtime candidate commit %q", p.Version, candidateCommit)
	}
	return nil
}

func (b RuntimeBinding) Sanitized() RuntimeBinding {
	b.Schema = audit.RedactString(b.Schema)
	b.Family = audit.RedactString(b.Family)
	b.Revision = audit.RedactString(b.Revision)
	b.ArtifactSHA256 = audit.RedactString(b.ArtifactSHA256)
	b.EnvironmentID = audit.RedactString(b.EnvironmentID)
	b.HostOS = audit.RedactString(b.HostOS)
	b.HostArch = audit.RedactString(b.HostArch)
	b.GuestArch = audit.RedactString(b.GuestArch)
	b.CandidateCommit = audit.RedactString(b.CandidateCommit)
	return b
}

func (b RuntimeBinding) Matches(expected RuntimeExpectation) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if err := b.Validate(); err != nil {
		return err
	}
	checks := []struct{ name, got, want string }{
		{"family", b.Family, expected.Family}, {"revision", b.Revision, expected.Revision},
		{"artifactSHA256", b.ArtifactSHA256, expected.ArtifactSHA256},
		{"hostOS", b.HostOS, expected.HostOS}, {"hostArch", b.HostArch, expected.HostArch},
		{"guestArch", b.GuestArch, expected.GuestArch},
		{"candidateCommit", b.CandidateCommit, expected.CandidateCommit},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.want) != "" && check.got != check.want {
			return fmt.Errorf("runtime evidence %s=%q does not match expected %q", check.name, check.got, check.want)
		}
	}
	if expected.RequireClean && b.CandidateDirty {
		return errors.New("runtime evidence candidate source state is dirty")
	}
	return nil
}

func (b RuntimeBinding) SameArtifactCandidate(other RuntimeBinding) bool {
	return b.Schema == other.Schema &&
		b.Family == other.Family &&
		b.Revision == other.Revision &&
		b.ArtifactSHA256 == other.ArtifactSHA256 &&
		b.HostOS == other.HostOS &&
		b.HostArch == other.HostArch &&
		b.GuestArch == other.GuestArch &&
		b.CandidateCommit == other.CandidateCommit &&
		b.CandidateDirty == other.CandidateDirty
}
