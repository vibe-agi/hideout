package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

const (
	runtimeObservationTimeout   = 20 * time.Second
	runtimeProcessOutputLimit   = 4 << 10
	runtimePackageInventoryPath = "/etc/hideout/package-inventory.txt"
)

func (b Backend) observeRuntime(ctx context.Context, session *backend.Session, runner CommandRunner, hostEnv, env []string, instance backend.RuntimeInstanceObservation) error {
	contract := session.RuntimeContract
	if contract == nil {
		return nil
	}
	if err := contract.Validate(); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, runtimeObservationTimeout)
	defer cancel()
	report := backend.RuntimeObservationReport{ContractID: contract.ID, ContractDigest: contract.Digest, PrivilegeStatus: "unknown", Instance: instance}
	if session.PrivilegeStatus != nil {
		report.PrivilegeStatus = string(session.PrivilegeStatus.Status)
	}
	for _, observation := range contract.Observations {
		result := backend.RuntimeObservationResult{ID: observation.ID, Class: observation.Class, Command: observation.Command}
		if err := runner.Run(probeCtx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, CommandCheck(observation.Command)), hostEnv, nil, io.Discard, io.Discard); err != nil {
			if probeCtx.Err() != nil {
				return probeCtx.Err()
			}
			result.Reason = "command-missing"
			report.Results = append(report.Results, result)
			appendRuntimeFailure(&report, observation)
			continue
		}
		result.Present = true
		result.Matched = true
		result.Reason = "ok"
		if len(observation.VersionArgs) > 0 {
			capture := &boundedRuntimeCapture{limit: runtimeProcessOutputLimit}
			command := append([]string{observation.Command}, observation.VersionArgs...)
			err := runner.Run(probeCtx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, command), hostEnv, nil, capture, capture)
			if probeCtx.Err() != nil {
				return probeCtx.Err()
			}
			result.Output = capture.String()
			switch {
			case err != nil:
				result.Matched = false
				result.Reason = "version-command-failed"
			case capture.truncated:
				result.Matched = false
				result.Reason = "output-limit-exceeded"
			case observation.OutputPattern != "" && !regexp.MustCompile(observation.OutputPattern).MatchString(strings.TrimSpace(result.Output)):
				result.Matched = false
				result.Reason = "version-mismatch"
			}
		}
		report.Results = append(report.Results, result)
		if !result.Present || !result.Matched {
			appendRuntimeFailure(&report, observation)
		}
	}
	if len(report.Results) != len(contract.Observations) {
		return errors.New("runtime observation report is incomplete")
	}
	if session.RuntimeResultSink != nil {
		if err := session.RuntimeResultSink(report); err != nil {
			return err
		}
	}
	if len(report.BoundaryFailed) > 0 {
		return backend.RuntimeBoundaryError{FailedIDs: append([]string(nil), report.BoundaryFailed...)}
	}
	return nil
}

type runtimeLimaInstance struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	VMType   string `json:"vmType"`
	Arch     string `json:"arch"`
	HostOS   string `json:"HostOS"`
	HostArch string `json:"HostArch"`
	Config   struct {
		VMType string `json:"vmType"`
		Arch   string `json:"arch"`
		Images []struct {
			Location string `json:"location"`
			Arch     string `json:"arch"`
			Digest   string `json:"digest"`
		} `json:"images"`
	} `json:"config"`
}

// InspectRuntimeInstance re-observes a running managed VM for status. It uses
// Lima's own instance inventory plus guest boot/architecture facts; callers do
// not get to supply any of the returned identity fields.
func InspectRuntimeInstance(ctx context.Context, instanceName string, expected backend.RuntimeInstanceExpectation) (backend.RuntimeInstanceObservation, error) {
	b := Backend{Stdout: io.Discard, Stderr: io.Discard}
	session := &backend.Session{
		ID: "runtime-status", EnvironmentID: "runtime-status", InstanceName: instanceName,
		GuestWork: "/", RuntimeInstanceExpected: &expected,
	}
	return b.inspectRuntimeInstance(ctx, b.runner(), HostCommandEnv(os.Environ()), session, true)
}

func (b Backend) inspectRuntimeInstance(ctx context.Context, runner CommandRunner, hostEnv []string, session *backend.Session, requireRunning bool) (backend.RuntimeInstanceObservation, error) {
	if session == nil || session.RuntimeInstanceExpected == nil {
		return backend.RuntimeInstanceObservation{}, errors.New("runtime instance expectation is required")
	}
	expected := *session.RuntimeInstanceExpected
	if err := expected.Validate(); err != nil {
		return backend.RuntimeInstanceObservation{}, err
	}
	var output bytes.Buffer
	args := []string{"list", "--format", "json", "--all-fields", session.InstanceName}
	if err := runner.Run(ctx, b.limactl(), args, hostEnv, nil, &output, io.Discard); err != nil {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("inspect Lima instance: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var matches []runtimeLimaInstance
	for {
		var info runtimeLimaInstance
		if err := decoder.Decode(&info); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return backend.RuntimeInstanceObservation{}, fmt.Errorf("decode Lima instance inventory: %w", err)
		}
		if info.Name == session.InstanceName {
			matches = append(matches, info)
		}
	}
	if len(matches) != 1 {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("expected one Lima instance %q, observed %d", session.InstanceName, len(matches))
	}
	info := matches[0]
	vmType := info.VMType
	if vmType == "" {
		vmType = info.Config.VMType
	}
	if vmType != expected.VMType || info.Config.VMType != expected.VMType {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima vmType mismatch: inventory=%q config=%q want=%q", info.VMType, info.Config.VMType, expected.VMType)
	}
	guestArch := info.Arch
	if guestArch == "" {
		guestArch = info.Config.Arch
	}
	if guestArch != expected.GuestArch {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima guest arch %q does not match %q", guestArch, expected.GuestArch)
	}
	if info.HostOS != expected.HostOS {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima host OS %q does not match %q", info.HostOS, expected.HostOS)
	}
	if info.HostArch != expected.HostArch {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima host arch %q does not match %q", info.HostArch, expected.HostArch)
	}
	imageMatches := 0
	for _, image := range info.Config.Images {
		if image.Arch == expected.GuestArch && image.Location == expected.ImageLocation && image.Digest == "sha256:"+expected.ImageSHA256 {
			imageMatches++
		}
	}
	if imageMatches != 1 {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima config does not bind exactly one expected image (matches=%d)", imageMatches)
	}
	// ImageLocation/ImageSHA256 describe the independently checked Lima config
	// binding. Active build identity is populated only from the running guest.
	observation := backend.RuntimeInstanceObservation{
		InstanceName: session.InstanceName, Status: info.Status, VMType: vmType,
		HostOS: expected.HostOS, HostArch: expected.HostArch, GuestArch: guestArch,
		ImageLocation: expected.ImageLocation, ImageSHA256: expected.ImageSHA256,
		SessionID: session.ID, EnvironmentID: session.EnvironmentID,
	}
	if !requireRunning {
		return observation, nil
	}
	if info.Status != "Running" {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima instance status %q is not Running", info.Status)
	}
	bootID, err := b.runtimeGuestFact(ctx, runner, hostEnv, session, []string{"cat", "/proc/sys/kernel/random/boot_id"})
	if err != nil {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("observe guest boot identity: %w", err)
	}
	observedArch, err := b.runtimeGuestFact(ctx, runner, hostEnv, session, []string{"uname", "-m"})
	if err != nil {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("observe guest architecture: %w", err)
	}
	if observedArch != expected.GuestArch {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("guest uname arch %q does not match %q", observedArch, expected.GuestArch)
	}
	packageInventorySHA256, err := b.runtimeGuestPackageInventorySHA256(ctx, runner, hostEnv, session)
	if err != nil {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("observe guest package inventory identity: %w", err)
	}
	if packageInventorySHA256 != expected.PackageInventorySHA256 {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("guest package inventory sha256 %q does not match selected runtime", packageInventorySHA256)
	}
	observation.BootID = bootID
	observation.PackageInventorySHA256 = packageInventorySHA256
	return observation, nil
}

func (b Backend) runtimeGuestPackageInventorySHA256(ctx context.Context, runner CommandRunner, hostEnv []string, session *backend.Session) (string, error) {
	capture := &boundedRuntimeCapture{limit: 256}
	args := []string{
		"shell", "--tty=false", "--workdir", "/", session.InstanceName, "--",
		"sha256sum", runtimePackageInventoryPath,
	}
	if err := runner.Run(ctx, b.limactl(), args, hostEnv, nil, capture, capture); err != nil {
		return "", err
	}
	if capture.truncated {
		return "", errors.New("package inventory identity output exceeded limit")
	}
	line := strings.TrimSuffix(capture.String(), "\n")
	if line == "" || strings.ContainsAny(line, "\r\n\x00") {
		return "", errors.New("package inventory identity output must contain exactly one line")
	}
	digest, path, ok := strings.Cut(line, "  ")
	if !ok || path != runtimePackageInventoryPath || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) {
		return "", errors.New("package inventory identity output is malformed")
	}
	return digest, nil
}

func (b Backend) runtimeGuestFact(ctx context.Context, runner CommandRunner, hostEnv []string, session *backend.Session, command []string) (string, error) {
	capture := &boundedRuntimeCapture{limit: 256}
	env := []string{"HOME=/hideout/profile/home", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, session.GuestWork, env, command), hostEnv, nil, capture, capture); err != nil {
		return "", err
	}
	if capture.truncated {
		return "", errors.New("guest fact exceeded output limit")
	}
	value := strings.TrimSpace(capture.String())
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("guest fact is empty or malformed")
	}
	return value, nil
}

func appendRuntimeFailure(report *backend.RuntimeObservationReport, observation backend.RuntimeObservation) {
	if observation.Class == backend.RuntimeObservationBoundary {
		report.BoundaryFailed = append(report.BoundaryFailed, observation.ID)
	} else {
		report.BaselineFailed = append(report.BaselineFailed, observation.ID)
	}
}

type boundedRuntimeCapture struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *boundedRuntimeCapture) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return original, nil
	}
	if len(p) > remaining {
		_, _ = w.buf.Write(p[:remaining])
		w.truncated = true
		return original, nil
	}
	_, _ = w.buf.Write(p)
	return original, nil
}

func (w *boundedRuntimeCapture) String() string { return w.buf.String() }
