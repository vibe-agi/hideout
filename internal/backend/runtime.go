package backend

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// RuntimeVerifier starts or reuses a prepared guest and observes only the
// runtime contract. It must not run guest setup, package installation, or a
// user target command.
type RuntimeVerifier interface {
	VerifyRuntime(ctx context.Context, session *Session, env []string) error
}

const (
	RuntimeObservationBoundary = "boundary"
	RuntimeObservationBaseline = "baseline"
)

type RuntimeContract struct {
	ID           string
	Digest       string
	Observations []RuntimeObservation
}

// RuntimeInstanceExpectation is the package-owned identity that a real
// backend must observe before a runtime report can be accepted. None of these
// values may be inferred from the backend name or copied from guest output.
type RuntimeInstanceExpectation struct {
	ImageLocation          string
	ImageSHA256            string
	PackageInventorySHA256 string
	HostOS                 string
	HostArch               string
	GuestArch              string
	VMType                 string
}

func (e RuntimeInstanceExpectation) Validate() error {
	if strings.TrimSpace(e.ImageLocation) == "" || len(e.ImageLocation) > 2048 ||
		!strings.HasPrefix(e.ImageLocation, "https://") || strings.ContainsAny(e.ImageLocation, "?#@\r\n") {
		return errors.New("runtime expected image location must be bounded credential-free HTTPS")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(e.ImageSHA256) {
		return errors.New("runtime expected image sha256 is invalid")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(e.PackageInventorySHA256) {
		return errors.New("runtime expected package inventory sha256 is invalid")
	}
	if e.HostOS != "darwin" || e.HostArch != "arm64" || e.GuestArch != "aarch64" || e.VMType != "vz" {
		return fmt.Errorf("runtime v1 requires darwin/arm64, aarch64, and VZ; got %s/%s/%s/%s", e.HostOS, e.HostArch, e.GuestArch, e.VMType)
	}
	return nil
}

// RuntimeInstanceObservation binds a report to the concrete running VM and
// boot that produced it. The backend owns these observations; target commands
// and policy scripts cannot supply them.
type RuntimeInstanceObservation struct {
	InstanceName           string
	Status                 string
	VMType                 string
	HostOS                 string
	HostArch               string
	GuestArch              string
	ImageLocation          string
	ImageSHA256            string
	PackageInventorySHA256 string
	BootID                 string
	SessionID              string
	EnvironmentID          string
}

type RuntimeObservation struct {
	ID            string
	Class         string
	Command       string
	VersionArgs   []string
	OutputPattern string
}

type RuntimeObservationResult struct {
	ID      string
	Class   string
	Command string
	Present bool
	Output  string
	Matched bool
	Reason  string
}

type RuntimeObservationReport struct {
	ContractID      string
	ContractDigest  string
	PrivilegeStatus string
	Instance        RuntimeInstanceObservation
	Results         []RuntimeObservationResult
	BoundaryFailed  []string
	BaselineFailed  []string
}

type RuntimeBoundaryError struct {
	FailedIDs []string
}

func (e RuntimeBoundaryError) Error() string {
	return "runtime boundary prerequisites failed: " + strings.Join(e.FailedIDs, ", ")
}

func (c RuntimeContract) Validate() error {
	if strings.TrimSpace(c.ID) == "" || len(c.ID) > 64 {
		return errors.New("runtime contract id is required and bounded")
	}
	if !strings.HasPrefix(c.Digest, "sha256:") || len(c.Digest) != len("sha256:")+64 {
		return errors.New("runtime contract digest is invalid")
	}
	if len(c.Observations) == 0 || len(c.Observations) > 64 {
		return errors.New("runtime contract requires 1-64 observations")
	}
	seen := map[string]bool{}
	for i, observation := range c.Observations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("runtime observation %d: %w", i, err)
		}
		if seen[observation.ID] {
			return fmt.Errorf("duplicate runtime observation %q", observation.ID)
		}
		seen[observation.ID] = true
	}
	return nil
}

func (o RuntimeObservation) Validate() error {
	if strings.TrimSpace(o.ID) == "" || len(o.ID) > 64 || containsRuntimeControl(o.ID) {
		return errors.New("runtime observation id is required and bounded")
	}
	if !slices.Contains([]string{RuntimeObservationBoundary, RuntimeObservationBaseline}, o.Class) {
		return fmt.Errorf("unsupported runtime observation class %q", o.Class)
	}
	if strings.TrimSpace(o.Command) == "" || len(o.Command) > 64 || strings.ContainsAny(o.Command, "/\\ \t\r\n;&|<>$=") {
		return fmt.Errorf("runtime observation command %q is not a simple command", o.Command)
	}
	if len(o.VersionArgs) > 4 {
		return errors.New("runtime observation has too many version args")
	}
	for _, arg := range o.VersionArgs {
		if arg == "" || len(arg) > 128 || containsRuntimeControl(arg) || arg == "-c" || arg == "--command" || strings.ContainsAny(arg, ";&|<>$=") {
			return fmt.Errorf("unsafe runtime version arg %q", arg)
		}
	}
	if len(o.OutputPattern) > 256 {
		return errors.New("runtime output pattern is too long")
	}
	if o.OutputPattern != "" {
		if !strings.HasPrefix(o.OutputPattern, "^") || !strings.HasSuffix(o.OutputPattern, "$") {
			return errors.New("runtime output pattern must be anchored")
		}
		if _, err := regexp.Compile(o.OutputPattern); err != nil {
			return fmt.Errorf("runtime output pattern: %w", err)
		}
	}
	return nil
}

func CloneRuntimeContract(contract *RuntimeContract) *RuntimeContract {
	if contract == nil {
		return nil
	}
	copy := *contract
	copy.Observations = append([]RuntimeObservation(nil), contract.Observations...)
	for i := range copy.Observations {
		copy.Observations[i].VersionArgs = append([]string(nil), copy.Observations[i].VersionArgs...)
	}
	return &copy
}

func containsRuntimeControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}
