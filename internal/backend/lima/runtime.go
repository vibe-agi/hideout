package lima

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

const (
	runtimeObservationTimeout   = 20 * time.Second
	runtimeProcessOutputLimit   = 4 << 10
	runtimePackageInventoryPath = "/etc/hideout/package-inventory.txt"
	runtimeBatchRecordPrefix    = "HIDEOUT_RUNTIME_V1"
	runtimeBatchEndPrefix       = "HIDEOUT_RUNTIME_END"
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
	results, err := b.observeRuntimeBatch(probeCtx, session, runner, hostEnv, env, contract)
	if err != nil {
		return err
	}
	for i, result := range results {
		observation := contract.Observations[i]
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

func (b Backend) observeRuntimeBatch(ctx context.Context, session *backend.Session, runner CommandRunner, hostEnv, env []string, contract *backend.RuntimeContract) ([]backend.RuntimeObservationResult, error) {
	if session == nil || contract == nil {
		return nil, errors.New("runtime batch observation requires a session and contract")
	}
	script := runtimeObservationBatchScript(contract.Observations)
	limit := len(contract.Observations)*(runtimeProcessOutputLimit+256) + 256
	stdout := &boundedRuntimeCapture{limit: limit}
	stderr := &boundedRuntimeCapture{limit: runtimeProcessOutputLimit}
	err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, "/", env, []string{"sh", "-c", script}), hostEnv, nil, stdout, stderr)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("runtime observation batch failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.truncated || stderr.truncated {
		return nil, errors.New("runtime observation batch exceeded its bounded output")
	}
	return parseRuntimeObservationBatch(stdout.buf.Bytes(), contract.Observations)
}

func runtimeObservationBatchScript(observations []backend.RuntimeObservation) string {
	var script strings.Builder
	script.WriteString("set +e\numask 077\n")
	script.WriteString("runtime_tmp=$(mktemp -d /hideout/session/tmp/runtime-observe.XXXXXX) || exit 97\n")
	script.WriteString("trap 'rm -rf \"$runtime_tmp\"' EXIT HUP INT TERM\n")
	for i, observation := range observations {
		command := shellQuote(observation.Command)
		fmt.Fprintf(&script, "if command -v %s >/dev/null 2>&1; then present=1; else present=0; fi\n", command)
		if len(observation.VersionArgs) == 0 {
			fmt.Fprintf(&script, "printf '%s %d %%s 0 0 0\\n\\n' \"$present\"\n", runtimeBatchRecordPrefix, i)
			continue
		}
		argv := []string{command}
		for _, arg := range observation.VersionArgs {
			argv = append(argv, shellQuote(arg))
		}
		fmt.Fprintf(&script, "out=$runtime_tmp/out.%d; status_file=$runtime_tmp/status.%d\n", i, i)
		script.WriteString("if [ \"$present\" -eq 1 ]; then\n")
		fmt.Fprintf(&script, "  ( %s; printf '%%s\\n' \"$?\" >\"$status_file\" ) 2>&1 | head -c %d >\"$out\"\n", strings.Join(argv, " "), runtimeProcessOutputLimit+1)
		script.WriteString("  if [ -s \"$status_file\" ]; then read -r status <\"$status_file\"; else status=1; fi\n")
		script.WriteString("else\n  : >\"$out\"; status=0\nfi\n")
		script.WriteString("set -- $(wc -c <\"$out\"); size=$1; emit=$size; truncated=0\n")
		fmt.Fprintf(&script, "if [ \"$emit\" -gt %d ]; then emit=%d; truncated=1; fi\n", runtimeProcessOutputLimit, runtimeProcessOutputLimit)
		fmt.Fprintf(&script, "printf '%s %d %%s %%s %%s %%s\\n' \"$present\" \"$status\" \"$truncated\" \"$emit\"\n", runtimeBatchRecordPrefix, i)
		script.WriteString("head -c \"$emit\" \"$out\"\nprintf '\\n'\n")
	}
	fmt.Fprintf(&script, "printf '%s %d\\n'\n", runtimeBatchEndPrefix, len(observations))
	return script.String()
}

func parseRuntimeObservationBatch(data []byte, observations []backend.RuntimeObservation) ([]backend.RuntimeObservationResult, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	results := make([]backend.RuntimeObservationResult, 0, len(observations))
	for i, observation := range observations {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("runtime observation %d header: %w", i, err)
		}
		fields := strings.Fields(strings.TrimSuffix(header, "\n"))
		if len(fields) != 6 || fields[0] != runtimeBatchRecordPrefix || fields[1] != strconv.Itoa(i) {
			return nil, fmt.Errorf("runtime observation %d has invalid frame header", i)
		}
		present, ok := parseRuntimeBatchBool(fields[2])
		if !ok {
			return nil, fmt.Errorf("runtime observation %d has invalid presence", i)
		}
		status, err := strconv.Atoi(fields[3])
		if err != nil || status < 0 || status > 255 {
			return nil, fmt.Errorf("runtime observation %d has invalid status", i)
		}
		truncated, ok := parseRuntimeBatchBool(fields[4])
		if !ok {
			return nil, fmt.Errorf("runtime observation %d has invalid truncation", i)
		}
		length, err := strconv.Atoi(fields[5])
		if err != nil || length < 0 || length > runtimeProcessOutputLimit {
			return nil, fmt.Errorf("runtime observation %d has invalid output length", i)
		}
		output := make([]byte, length)
		if _, err := io.ReadFull(reader, output); err != nil {
			return nil, fmt.Errorf("runtime observation %d output: %w", i, err)
		}
		separator, err := reader.ReadByte()
		if err != nil || separator != '\n' {
			return nil, fmt.Errorf("runtime observation %d has invalid frame terminator", i)
		}
		if !present && (status != 0 || truncated || length != 0) {
			return nil, fmt.Errorf("runtime observation %d missing-command frame is inconsistent", i)
		}
		if len(observation.VersionArgs) == 0 && (status != 0 || truncated || length != 0) {
			return nil, fmt.Errorf("runtime observation %d presence-only frame has output", i)
		}
		result := backend.RuntimeObservationResult{
			ID: observation.ID, Class: observation.Class, Command: observation.Command,
			Present: present, Output: string(output), Matched: present, Reason: "ok",
		}
		switch {
		case !present:
			result.Reason = "command-missing"
		case truncated:
			result.Matched = false
			result.Reason = "output-limit-exceeded"
		case status != 0:
			result.Matched = false
			result.Reason = "version-command-failed"
		case observation.OutputPattern != "" && !regexp.MustCompile(observation.OutputPattern).MatchString(strings.TrimSpace(result.Output)):
			result.Matched = false
			result.Reason = "version-mismatch"
		}
		results = append(results, result)
	}
	end, err := reader.ReadString('\n')
	if err != nil || strings.TrimSuffix(end, "\n") != runtimeBatchEndPrefix+" "+strconv.Itoa(len(observations)) {
		return nil, errors.New("runtime observation batch has invalid end frame")
	}
	if trailing, _ := io.ReadAll(reader); len(trailing) != 0 {
		return nil, errors.New("runtime observation batch has trailing data")
	}
	return results, nil
}

func parseRuntimeBatchBool(value string) (bool, bool) {
	switch value {
	case "0":
		return false, true
	case "1":
		return true, true
	default:
		return false, false
	}
}

type runtimeLimaInstance struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	VMType   string `json:"vmType"`
	Arch     string `json:"arch"`
	HostOS   string `json:"HostOS"`
	HostArch string `json:"HostArch"`
	Config   struct {
		VMType string             `json:"vmType"`
		Arch   string             `json:"arch"`
		Images []runtimeLimaImage `json:"images"`
	} `json:"config"`
}

type runtimeLimaImage struct {
	Location string `json:"location"`
	Arch     string `json:"arch"`
	Digest   string `json:"digest"`
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
	if normalizeLimaHostArch(info.HostArch) != expected.HostArch {
		return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima host arch %q does not match %q", info.HostArch, expected.HostArch)
	}
	imageMatches := 0
	for _, image := range info.Config.Images {
		if image.Arch == expected.GuestArch && image.Location == expected.ImageLocation && image.Digest == "sha256:"+expected.ImageSHA256 {
			imageMatches++
		}
	}
	if imageMatches != 1 {
		importedMatch, importedErr := b.importedRuntimeImageMatches(
			hostEnv, session, info.Config.Images,
		)
		if importedErr != nil {
			return backend.RuntimeInstanceObservation{}, fmt.Errorf("verify imported Lima runtime image provenance: %w", importedErr)
		}
		if !importedMatch {
			return backend.RuntimeInstanceObservation{}, fmt.Errorf("Lima config does not bind exactly one expected image (matches=%d)", imageMatches)
		}
	}
	// ImageLocation/ImageSHA256 describe either the independently checked Lima
	// config binding or the authenticated import marker retained beside a
	// fail-closed image sentinel. Active build identity is populated only from
	// the running guest.
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

func normalizeLimaHostArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "arm64", "aarch64":
		return "arm64"
	case "amd64", "x86_64":
		return "amd64"
	default:
		return value
	}
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
	if err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, "/", env, command), hostEnv, nil, capture, capture); err != nil {
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
