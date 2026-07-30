//go:build ignore

// Production mutation runner for feature 045.
//
// The runner never edits the checkout. Each mutant replaces one exact source
// fragment in a private temporary file and asks `go test -overlay` to compile
// that file in place of the production source. A compile error is not a killed
// mutant: at least one explicitly named direct assertion must execute and fail.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	manifestSchema = "hideout.045-production-mutations/v1"
	summarySchema  = "hideout.045-production-mutation-run/v1"
)

var (
	mutationIDPattern = regexp.MustCompile(`^M045-([A-Z]+[0-9]{2})(?:[A-Z])?$`)
	claimIDPattern    = regexp.MustCompile(`^(?:A|AT|R|C|H|U|RC|CL)[0-9]{2}$`)
)

type manifest struct {
	Schema    string     `json:"schema"`
	Mutations []mutation `json:"mutations"`
}

type mutation struct {
	ID             string   `json:"id"`
	ClaimID        string   `json:"claimId"`
	Description    string   `json:"description"`
	Source         string   `json:"source"`
	From           string   `json:"from"`
	To             string   `json:"to"`
	Packages       []string `json:"packages"`
	Tests          string   `json:"tests"`
	KillTests      []string `json:"killTests"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

type contracts struct {
	Schema string `json:"schema"`
	Claims []struct {
		ID           string          `json:"id"`
		Requirements json.RawMessage `json:"requirements"`
		Judges       json.RawMessage `json:"judges"`
		Negative     json.RawMessage `json:"negative"`
	} `json:"claims"`
}

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type commandEvidence struct {
	Command     []string `json:"command"`
	Result      string   `json:"result"`
	ExitCode    int      `json:"exitCode"`
	ElapsedMS   int64    `json:"elapsedMs"`
	Log         string   `json:"log"`
	LogSHA256   string   `json:"logSHA256"`
	PassedTests []string `json:"passedTests,omitempty"`
	FailedTests []string `json:"failedTests,omitempty"`
}

type proof struct {
	ID          string          `json:"id"`
	ClaimID     string          `json:"claimId"`
	Description string          `json:"description"`
	Source      string          `json:"source"`
	FromSHA256  string          `json:"fromSHA256"`
	ToSHA256    string          `json:"toSHA256"`
	Baseline    commandEvidence `json:"baseline"`
	Mutant      commandEvidence `json:"mutant"`
	Result      string          `json:"result"`
}

type runSummary struct {
	Schema         string   `json:"schema"`
	GeneratedAt    string   `json:"generatedAt"`
	Result         string   `json:"result"`
	Manifest       string   `json:"manifest"`
	ManifestSHA256 string   `json:"manifestSHA256"`
	Contracts      string   `json:"contracts"`
	ContractsSHA   string   `json:"contractsSHA256"`
	SelectedClaim  string   `json:"selectedClaim,omitempty"`
	RequiredClaims int      `json:"requiredClaims"`
	Executed       int      `json:"executed"`
	Killed         int      `json:"killed"`
	Proofs         []proof  `json:"proofs"`
	Errors         []string `json:"errors,omitempty"`
}

func main() {
	var (
		rootPath      string
		manifestPath  string
		contractsPath string
		outPath       string
		selectedClaim string
	)
	flag.StringVar(&rootPath, "root", "", "absolute repository root")
	flag.StringVar(&manifestPath, "manifest", "", "production mutation manifest")
	flag.StringVar(&contractsPath, "contracts", "", "claim contracts")
	flag.StringVar(&outPath, "out", "", "private evidence directory")
	flag.StringVar(&selectedClaim, "claim", "", "run one claim during development")
	flag.Parse()

	if err := run(rootPath, manifestPath, contractsPath, outPath, selectedClaim); err != nil {
		fmt.Fprintf(os.Stderr, "045-production-mutations: %v\n", err)
		os.Exit(1)
	}
}

func run(rootPath, manifestPath, contractsPath, outPath, selectedClaim string) error {
	root, err := requireAbsoluteDir(rootPath)
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}
	out, err := requireAbsolutePath(outPath)
	if err != nil {
		return fmt.Errorf("out: %w", err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(out, 0o700); err != nil {
		return err
	}

	loadedManifest, manifestBytes, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	required, contractsBytes, err := loadContracts(contractsPath)
	if err != nil {
		return err
	}
	if err := validateManifest(root, loadedManifest, required, selectedClaim); err != nil {
		return err
	}

	selected := loadedManifest.Mutations
	if selectedClaim != "" {
		selected = nil
		for _, item := range loadedManifest.Mutations {
			if item.ClaimID == selectedClaim {
				selected = append(selected, item)
			}
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].ID < selected[j].ID
	})

	summary := runSummary{
		Schema:         summarySchema,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Result:         "failed",
		Manifest:       relativeOrClean(root, manifestPath),
		ManifestSHA256: digestBytes(manifestBytes),
		Contracts:      relativeOrClean(root, contractsPath),
		ContractsSHA:   digestBytes(contractsBytes),
		SelectedClaim:  selectedClaim,
		RequiredClaims: len(required),
		Proofs:         make([]proof, 0, len(selected)),
	}

	for _, item := range selected {
		fmt.Printf("045-production-mutations: running %s (%s)\n", item.ID, item.ClaimID)
		itemProof, proofErr := executeMutation(root, out, item)
		summary.Executed++
		summary.Proofs = append(summary.Proofs, itemProof)
		if proofErr != nil {
			summary.Errors = append(summary.Errors, item.ID+": "+proofErr.Error())
			fmt.Printf("045-production-mutations: %s survived or was invalid\n", item.ID)
			continue
		}
		summary.Killed++
		fmt.Printf("045-production-mutations: %s killed\n", item.ID)
	}
	if summary.Executed == len(selected) && summary.Killed == len(selected) &&
		len(summary.Errors) == 0 {
		summary.Result = "passed"
	}
	if err := writeJSONPrivate(filepath.Join(out, "summary.json"), summary); err != nil {
		return err
	}
	if summary.Result != "passed" {
		return fmt.Errorf(
			"result=failed executed=%d killed=%d evidence=%s",
			summary.Executed,
			summary.Killed,
			filepath.Join(out, "summary.json"),
		)
	}
	fmt.Printf(
		"045-production-mutations: passed executed=%d killed=%d evidence=%s\n",
		summary.Executed,
		summary.Killed,
		filepath.Join(out, "summary.json"),
	)
	return nil
}

func executeMutation(root, out string, item mutation) (proof, error) {
	result := proof{
		ID: item.ID, ClaimID: item.ClaimID,
		Description: item.Description, Source: item.Source,
		FromSHA256: digestBytes([]byte(item.From)),
		ToSHA256:   digestBytes([]byte(item.To)),
		Result:     "survived",
	}
	caseDir := filepath.Join(out, item.ID)
	if err := os.MkdirAll(caseDir, 0o700); err != nil {
		return result, err
	}
	if err := os.Chmod(caseDir, 0o700); err != nil {
		return result, err
	}

	baselineArgs := testArgs("", item)
	result.Baseline = runGoTest(
		root,
		baselineArgs,
		filepath.Join(caseDir, "baseline.log"),
		item.TimeoutSeconds,
	)
	if result.Baseline.ExitCode != 0 ||
		result.Baseline.Result != "passed" ||
		!intersects(result.Baseline.PassedTests, item.KillTests) {
		return result, fmt.Errorf(
			"baseline direct assertion did not execute and pass: passed=%v expected=%v",
			result.Baseline.PassedTests,
			item.KillTests,
		)
	}

	sourcePath := filepath.Join(root, filepath.FromSlash(item.Source))
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return result, err
	}
	if count := bytes.Count(sourceBytes, []byte(item.From)); count != 1 {
		return result, fmt.Errorf("source fragment occurrence count=%d, want 1", count)
	}
	mutated := bytes.Replace(sourceBytes, []byte(item.From), []byte(item.To), 1)
	mutatedPath := filepath.Join(caseDir, filepath.Base(item.Source)+".mutant")
	if err := os.WriteFile(mutatedPath, mutated, 0o600); err != nil {
		return result, err
	}
	overlayPath := filepath.Join(caseDir, "overlay.json")
	overlay := struct {
		Replace map[string]string `json:"Replace"`
	}{
		Replace: map[string]string{
			sourcePath: mutatedPath,
		},
	}
	if err := writeJSONPrivate(overlayPath, overlay); err != nil {
		return result, err
	}

	mutantArgs := testArgs(overlayPath, item)
	result.Mutant = runGoTest(
		root,
		mutantArgs,
		filepath.Join(caseDir, "mutant.log"),
		item.TimeoutSeconds,
	)
	expectedFailure := intersects(result.Mutant.FailedTests, item.KillTests)
	if result.Mutant.ExitCode == 0 ||
		result.Mutant.Result != "failed" ||
		!expectedFailure {
		return result, fmt.Errorf(
			"mutant did not fail an expected direct assertion: failed=%v expected=%v",
			result.Mutant.FailedTests,
			item.KillTests,
		)
	}
	result.Result = "killed"
	return result, nil
}

func testArgs(overlay string, item mutation) []string {
	args := []string{
		"test",
		"-json",
		"-p=1",
		"-count=1",
		"-run=" + item.Tests,
	}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	return append(args, item.Packages...)
}

func runGoTest(
	root string,
	args []string,
	logPath string,
	timeoutSeconds int,
) commandEvidence {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(timeoutSeconds)*time.Second,
	)
	defer cancel()
	start := time.Now()
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	elapsed := time.Since(start)
	_ = os.WriteFile(logPath, output, 0o600)
	_ = os.Chmod(logPath, 0o600)

	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	if ctx.Err() != nil {
		exitCode = 124
	}
	passed, failed := testResults(output)
	result := "passed"
	if exitCode != 0 {
		result = "failed"
	}
	return commandEvidence{
		Command: append([]string{"go"}, args...),
		Result:  result, ExitCode: exitCode,
		ElapsedMS:   elapsed.Milliseconds(),
		Log:         filepath.ToSlash(logPath),
		LogSHA256:   digestBytes(output),
		PassedTests: passed,
		FailedTests: failed,
	}
}

func testResults(output []byte) ([]string, []string) {
	passed := map[string]struct{}{}
	failed := map[string]struct{}{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	const maxEventBytes = 4 << 20
	scanner.Buffer(make([]byte, 64<<10), maxEventBytes)
	for scanner.Scan() {
		var event testEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Test == "" {
			continue
		}
		switch event.Action {
		case "pass":
			passed[event.Test] = struct{}{}
		case "fail":
			failed[event.Test] = struct{}{}
		}
	}
	passedList := make([]string, 0, len(passed))
	for test := range passed {
		passedList = append(passedList, test)
	}
	failedList := make([]string, 0, len(failed))
	for test := range failed {
		failedList = append(failedList, test)
	}
	sort.Strings(passedList)
	sort.Strings(failedList)
	return passedList, failedList
}

func validateManifest(
	root string,
	value manifest,
	required map[string]struct{},
	selectedClaim string,
) error {
	if value.Schema != manifestSchema {
		return fmt.Errorf("manifest schema=%q", value.Schema)
	}
	if len(value.Mutations) == 0 {
		return errors.New("manifest has no production mutations")
	}
	if selectedClaim != "" {
		if !claimIDPattern.MatchString(selectedClaim) {
			return fmt.Errorf("selected claim %q is invalid", selectedClaim)
		}
		if _, ok := required[selectedClaim]; !ok {
			return fmt.Errorf("selected claim %q is not required", selectedClaim)
		}
	}
	ids := map[string]struct{}{}
	claims := map[string]struct{}{}
	for index, item := range value.Mutations {
		prefix := fmt.Sprintf("mutations[%d]", index)
		if !mutationIDPattern.MatchString(item.ID) {
			return fmt.Errorf("%s id=%q is invalid", prefix, item.ID)
		}
		if !claimIDPattern.MatchString(item.ClaimID) {
			return fmt.Errorf("%s claimId=%q is invalid", prefix, item.ClaimID)
		}
		if _, ok := required[item.ClaimID]; !ok {
			return fmt.Errorf("%s claimId=%q is not required", prefix, item.ClaimID)
		}
		if _, ok := ids[item.ID]; ok {
			return fmt.Errorf("%s duplicate id=%q", prefix, item.ID)
		}
		ids[item.ID] = struct{}{}
		if _, ok := claims[item.ClaimID]; ok {
			return fmt.Errorf("%s duplicate claimId=%q", prefix, item.ClaimID)
		}
		claims[item.ClaimID] = struct{}{}
		if strings.TrimSpace(item.Description) == "" ||
			item.From == "" || item.To == "" || item.From == item.To ||
			strings.TrimSpace(item.Tests) == "" ||
			len(item.Packages) == 0 || len(item.KillTests) == 0 {
			return fmt.Errorf("%s has an empty required field", prefix)
		}
		if err := validateRelativeSource(root, item.Source); err != nil {
			return fmt.Errorf("%s source: %w", prefix, err)
		}
		for _, pkg := range item.Packages {
			if !strings.HasPrefix(pkg, "./") || strings.Contains(pkg, "..") {
				return fmt.Errorf("%s package=%q is outside the repository", prefix, pkg)
			}
		}
		for _, test := range item.KillTests {
			if !strings.HasPrefix(test, "Test") {
				return fmt.Errorf("%s kill test=%q is invalid", prefix, test)
			}
		}
	}
	if selectedClaim != "" {
		if _, ok := claims[selectedClaim]; !ok {
			return fmt.Errorf("selected claim %q has no mutation", selectedClaim)
		}
		return nil
	}
	missing := make([]string, 0)
	for claim := range required {
		if _, ok := claims[claim]; !ok {
			missing = append(missing, claim)
		}
	}
	extra := make([]string, 0)
	for claim := range claims {
		if _, ok := required[claim]; !ok {
			extra = append(extra, claim)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) != 0 || len(extra) != 0 {
		return fmt.Errorf(
			"claim coverage mismatch missing=%v extra=%v",
			missing,
			extra,
		)
	}
	return nil
}

func validateRelativeSource(root, value string) error {
	if value == "" || filepath.IsAbs(value) ||
		filepath.Clean(value) != filepath.FromSlash(value) ||
		strings.HasPrefix(value, ".."+string(filepath.Separator)) ||
		filepath.Ext(value) != ".go" {
		return fmt.Errorf("%q is not a normalized relative Go source", value)
	}
	path := filepath.Join(root, value)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("source is not a regular non-symlink file")
	}
	return nil
}

func loadManifest(path string) (manifest, []byte, error) {
	var value manifest
	data, err := readPrivateInput(path)
	if err != nil {
		return value, nil, err
	}
	if err := strictJSON(data, &value); err != nil {
		return value, nil, fmt.Errorf("manifest: %w", err)
	}
	return value, data, nil
}

func loadContracts(path string) (map[string]struct{}, []byte, error) {
	var value contracts
	data, err := readPrivateInput(path)
	if err != nil {
		return nil, nil, err
	}
	if err := strictJSON(data, &value); err != nil {
		return nil, nil, fmt.Errorf("contracts: %w", err)
	}
	if value.Schema != "hideout.045-claim-judge-contracts/v1" {
		return nil, nil, fmt.Errorf("contracts schema=%q", value.Schema)
	}
	required := make(map[string]struct{}, len(value.Claims))
	for _, claim := range value.Claims {
		if !claimIDPattern.MatchString(claim.ID) {
			return nil, nil, fmt.Errorf("contracts claim id=%q is invalid", claim.ID)
		}
		if _, ok := required[claim.ID]; ok {
			return nil, nil, fmt.Errorf("contracts duplicate claim id=%q", claim.ID)
		}
		required[claim.ID] = struct{}{}
	}
	if len(required) == 0 {
		return nil, nil, errors.New("contracts has no claims")
	}
	return required, data, nil
}

func readPrivateInput(path string) ([]byte, error) {
	clean, err := requireAbsolutePath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("input is not a regular non-symlink file")
	}
	return os.ReadFile(clean)
}

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeJSONPrivate(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func requireAbsoluteDir(path string) (string, error) {
	clean, err := requireAbsolutePath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return clean, nil
}

func requireAbsolutePath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) {
		return "", errors.New("filesystem root is not allowed")
	}
	return clean, nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func relativeOrClean(root, path string) string {
	clean, err := requireAbsolutePath(path)
	if err != nil {
		return filepath.Clean(path)
	}
	relative, err := filepath.Rel(root, clean)
	if err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return clean
}

func intersects(left, right []string) bool {
	expected := make(map[string]struct{}, len(right))
	for _, value := range right {
		expected[value] = struct{}{}
	}
	for _, value := range left {
		if _, ok := expected[value]; ok {
			return true
		}
	}
	return false
}
