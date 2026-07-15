package hostcap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

const (
	BindingAccessSafe       = "safe"
	BindingAccessAskEachRun = "ask-each-run"
	BindingGrammarV1        = "open-resource-v1"
)

var bindingDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var bindingCommand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}$`)

// BindingGrammar is syntax-only package data compiled into a run binding. It
// contains no application, host path, executable, or capability authority.
type BindingGrammar struct {
	Kind             string
	ResourceCount    int
	GotoFlags        []string
	NewWindowFlags   []string
	ReuseWindowFlags []string
	UnknownFlags     string
}

// OpenResourceBinding is the immutable Core interpretation of one enabled
// package binding. Guest input can select only a registered command. Every
// authority-bearing field below is derived from the enabled revision and Core
// catalogs before a run starts. Safe bindings may attach the path-bearing
// observed application identity on first command use; that observation cannot
// change the binding's static authority digest.
type OpenResourceBinding struct {
	PackID                string
	RevisionID            string
	BindingID             string
	QualifiedAppRef       string
	Commands              []string
	CapabilityID          string
	ResourceKinds         []ResourceKind
	ResultPolicy          ResultPolicy
	Access                string
	Grammar               BindingGrammar
	Application           ApplicationExpectation
	Launch                appopen.LaunchSpec
	SafetyProfileID       string
	SafetyProfileVersion  string
	SourceDigest          string
	PermissionFingerprint string
	// IdentityDeferred keeps application observation out of unrelated run
	// startup. ExpectedIdentitySetDigest binds community enablement to the
	// identity set approved by the operator; built-in bindings instead rely on
	// Core-owned application expectations and safety profiles.
	IdentityDeferred          bool
	ExpectedIdentitySetDigest string
	ObservedIdentityDigest    string
	ObservedIdentity          ObservedApplicationIdentity
	BindingDigest             string
}

// BindingCatalog is an immutable command-to-binding map. It has no fallback:
// an unregistered command cannot acquire a host application or capability.
type BindingCatalog struct {
	byCommand map[string]OpenResourceBinding
}

func NewBindingCatalog(bindings []OpenResourceBinding) (BindingCatalog, error) {
	catalog := BindingCatalog{byCommand: map[string]OpenResourceBinding{}}
	for _, candidate := range bindings {
		binding := cloneOpenResourceBinding(candidate)
		if err := ValidateOpenResourceBinding(binding); err != nil {
			return BindingCatalog{}, err
		}
		for _, command := range binding.Commands {
			if _, exists := catalog.byCommand[command]; exists {
				return BindingCatalog{}, fmt.Errorf("hostcap: projected command %q has multiple immutable owners", command)
			}
			catalog.byCommand[command] = binding
		}
	}
	return catalog, nil
}

func (c BindingCatalog) ResolveCommand(command string) (OpenResourceBinding, bool) {
	if !validBindingCommand(command) {
		return OpenResourceBinding{}, false
	}
	binding, ok := c.byCommand[command]
	return cloneOpenResourceBinding(binding), ok
}

func (c BindingCatalog) Bindings() []OpenResourceBinding {
	seen := map[string]OpenResourceBinding{}
	for _, binding := range c.byCommand {
		key := binding.PackID + "\x00" + binding.RevisionID + "\x00" + binding.BindingID
		seen[key] = binding
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]OpenResourceBinding, 0, len(keys))
	for _, key := range keys {
		out = append(out, cloneOpenResourceBinding(seen[key]))
	}
	return out
}

func ValidateOpenResourceBinding(binding OpenResourceBinding) error {
	for label, value := range map[string]string{
		"pack": binding.PackID, "revision": binding.RevisionID,
		"binding": binding.BindingID, "qualified app": binding.QualifiedAppRef,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("hostcap: immutable %s identity is invalid", label)
		}
	}
	if binding.Application.QualifiedAppRef != binding.QualifiedAppRef {
		return errors.New("hostcap: application expectation does not match the immutable qualified app")
	}
	if binding.CapabilityID != CapabilityAppOpenResource {
		return errors.New("hostcap: binding capability is not host.app.open-resource")
	}
	if binding.ResultPolicy != ResultNone {
		return errors.New("hostcap: open-resource binding must have no host-to-guest result channel")
	}
	if binding.Access != BindingAccessSafe && binding.Access != BindingAccessAskEachRun {
		return errors.New("hostcap: binding access must be safe or ask-each-run")
	}
	if binding.Access == BindingAccessSafe && (binding.SafetyProfileID == "" || binding.SafetyProfileVersion == "") {
		return errors.New("hostcap: safe binding requires an exact Core safety profile id and version")
	}
	if binding.Access == BindingAccessAskEachRun && (binding.SafetyProfileID != "" || binding.SafetyProfileVersion != "") {
		return errors.New("hostcap: ask-each-run binding must not claim a Core safe profile")
	}
	if len(binding.Commands) == 0 || len(binding.Commands) > 16 {
		return errors.New("hostcap: binding requires 1-16 commands")
	}
	commands := map[string]bool{}
	for _, command := range binding.Commands {
		if !validBindingCommand(command) {
			return errors.New("hostcap: binding command must be a simple name")
		}
		if commands[command] {
			return errors.New("hostcap: binding command is duplicated")
		}
		commands[command] = true
	}
	if len(binding.ResourceKinds) == 0 {
		return errors.New("hostcap: binding has no resource kind")
	}
	seenKinds := map[ResourceKind]bool{}
	for _, kind := range binding.ResourceKinds {
		if kind != KindWorkspace && kind != KindHostFS {
			return fmt.Errorf("hostcap: unsupported open-resource kind %q", kind)
		}
		if seenKinds[kind] {
			return fmt.Errorf("hostcap: duplicate open-resource kind %q", kind)
		}
		seenKinds[kind] = true
	}
	if binding.Grammar.Kind != BindingGrammarV1 || binding.Grammar.ResourceCount != 1 || binding.Grammar.UnknownFlags != "deny" {
		return errors.New("hostcap: unsupported or missing immutable grammar")
	}
	if !bindingDigest.MatchString(binding.SourceDigest) || !bindingDigest.MatchString(binding.PermissionFingerprint) {
		return errors.New("hostcap: binding must carry exact source and permission digests")
	}
	if binding.IdentityDeferred {
		if binding.ExpectedIdentitySetDigest != "" && !bindingDigest.MatchString(binding.ExpectedIdentitySetDigest) {
			return errors.New("hostcap: deferred binding expected identity-set digest is invalid")
		}
		if (binding.ObservedIdentityDigest == "") != (binding.ObservedIdentity.IdentityDigest() == "") {
			return errors.New("hostcap: deferred binding carries a partial observed identity")
		}
		if binding.ObservedIdentityDigest != "" {
			if err := validateBindingObservedIdentity(binding); err != nil {
				return err
			}
		}
	} else {
		if binding.ExpectedIdentitySetDigest != "" {
			return errors.New("hostcap: eager binding cannot carry a deferred identity-set digest")
		}
		if err := validateBindingObservedIdentity(binding); err != nil {
			return err
		}
	}
	wantDigest, err := ComputeBindingDigest(binding)
	if err != nil || binding.BindingDigest != wantDigest {
		return errors.New("hostcap: immutable binding digest is missing or stale")
	}
	return nil
}

func validateBindingObservedIdentity(binding OpenResourceBinding) error {
	if !bindingDigest.MatchString(binding.ObservedIdentityDigest) || binding.ObservedIdentity.IdentityDigest() != binding.ObservedIdentityDigest || binding.ObservedIdentity.QualifiedAppRef != binding.QualifiedAppRef {
		return errors.New("hostcap: compiled-run binding must carry the exact Core-observed app identity")
	}
	if binding.ObservedIdentity.ExecutableRelativePath != filepath.ToSlash(binding.Application.ExecutableRelativePath) || binding.ObservedIdentity.ExecutableCodeIdentity == "" {
		return errors.New("hostcap: compiled-run binding executable identity does not match the reviewed application executable")
	}
	return nil
}

func validBindingCommand(command string) bool {
	return len(command) <= 64 && bindingCommand.MatchString(command) && filepath.Base(command) == command
}

// ComputeBindingDigest hashes only path-free immutable authority. Eager
// bindings include their observed identity digest. Deferred bindings include
// the approved identity-set digest, when one exists, and attach the current
// observed identity only inside Core on first use. Host paths and executable
// locations never enter guest data.
func ComputeBindingDigest(binding OpenResourceBinding) (string, error) {
	canonical := struct {
		PackID, RevisionID, BindingID, QualifiedAppRef string
		Commands                                       []string
		CapabilityID                                   string
		ResourceKinds                                  []ResourceKind
		ResultPolicy                                   ResultPolicy
		Access                                         string
		Grammar                                        BindingGrammar
		Application                                    ApplicationExpectation
		Launch                                         appopen.LaunchSpec
		SafetyProfileID, SafetyProfileVersion          string
		SourceDigest, PermissionFingerprint            string
		IdentityDeferred                               bool
		ExpectedIdentitySetDigest                      string
		ObservedIdentityDigest                         string
	}{
		binding.PackID, binding.RevisionID, binding.BindingID, binding.QualifiedAppRef,
		append([]string(nil), binding.Commands...), binding.CapabilityID,
		append([]ResourceKind(nil), binding.ResourceKinds...), binding.ResultPolicy, binding.Access,
		binding.Grammar, binding.Application, binding.Launch,
		binding.SafetyProfileID, binding.SafetyProfileVersion,
		binding.SourceDigest, binding.PermissionFingerprint,
		binding.IdentityDeferred, binding.ExpectedIdentitySetDigest, binding.ObservedIdentityDigest,
	}
	if binding.IdentityDeferred {
		// The guest-visible binding is stable before any host application is
		// touched. The observed identity is attached inside Core on first use and
		// checked separately against ExpectedIdentitySetDigest when present.
		canonical.ObservedIdentityDigest = ""
	}
	sort.Strings(canonical.Commands)
	sort.Slice(canonical.ResourceKinds, func(i, j int) bool { return canonical.ResourceKinds[i] < canonical.ResourceKinds[j] })
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func FinalizeBindingDigest(binding OpenResourceBinding) (OpenResourceBinding, error) {
	binding.BindingDigest = ""
	digest, err := ComputeBindingDigest(binding)
	if err != nil {
		return OpenResourceBinding{}, err
	}
	binding.BindingDigest = digest
	return binding, nil
}

func cloneOpenResourceBinding(binding OpenResourceBinding) OpenResourceBinding {
	binding.Commands = append([]string(nil), binding.Commands...)
	binding.ResourceKinds = append([]ResourceKind(nil), binding.ResourceKinds...)
	binding.Grammar.GotoFlags = append([]string(nil), binding.Grammar.GotoFlags...)
	binding.Grammar.NewWindowFlags = append([]string(nil), binding.Grammar.NewWindowFlags...)
	binding.Grammar.ReuseWindowFlags = append([]string(nil), binding.Grammar.ReuseWindowFlags...)
	binding.Application.BundleNames = append([]string(nil), binding.Application.BundleNames...)
	binding.Launch.SafeIsolationFlags = append([]string(nil), binding.Launch.SafeIsolationFlags...)
	binding.Launch.ForbiddenFlags = append([]string(nil), binding.Launch.ForbiddenFlags...)
	return binding
}

func bindingAllowsResource(binding OpenResourceBinding, kind ResourceKind) bool {
	for _, allowed := range binding.ResourceKinds {
		if allowed == kind {
			return true
		}
	}
	return false
}
