package profiletemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostpathrisk"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	EvidenceVersion = "hideout.onboarding-evidence/v1"

	Privacy  = "privacy"
	Hardened = "hardened"
	Dev      = "dev"
	Debug    = "debug"

	PrivilegeEnforced = "enforced"
	PrivilegeDegraded = "degraded"
	PrivilegeUnknown  = "unknown"

	VisibilityNone      = "none"
	VisibilityLandmarks = "landmarks"
	VisibilityHomeTree  = "home-tree"
)

type Template struct {
	ID                   string
	Recommended          bool
	Description          string
	DefaultNetwork       string
	RequiresTun2socks    bool
	RequiresPrivilege    string
	EffectivePosture     string
	HostFSPosture        string
	AdapterPackPosture   string
	Warnings             []string
	NonClaims            []string
	FollowUps            []string
	DevelopmentOnlyLabel string
}

type PrivilegeFact struct {
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Guidance string `json:"guidance,omitempty"`
	Source   string `json:"source,omitempty"`
}

type Request struct {
	ProfileName                string
	TemplateID                 string
	Backend                    string
	Network                    string
	ProxySecretRef             string
	MediatedResolver           string
	Privilege                  PrivilegeFact
	AllowDegradedTemplate      bool
	NoInput                    bool
	ExplicitProfile            bool
	ExplicitTemplate           bool
	ExplicitBackend            bool
	ExplicitNetwork            bool
	CheckExistingProfile       bool
	Store                      profile.Store
	Now                        time.Time
	AdditionalWarning          string
	AdditionalNonClaim         string
	VisibilitySelection        string
	VisibilityRoots            []string
	NameDisclosureAcknowledged bool
	ExplicitVisibility         bool
	HomeDir                    string
}

type Rendered struct {
	Template          Template
	Profile           profile.Profile
	EffectivePosture  string
	Warnings          []string
	NonClaims         []string
	Review            Review
	Evidence          EvidenceSummary
	EvidencePath      string
	VisibilityRuleIDs []string
}

type Review struct {
	Lines []string `json:"lines"`
}

type EvidenceSummary struct {
	Version                    string        `json:"version"`
	Time                       time.Time     `json:"time"`
	Profile                    string        `json:"profile"`
	Template                   string        `json:"template"`
	EffectivePosture           string        `json:"effectivePosture"`
	Backend                    string        `json:"backend"`
	Network                    string        `json:"network"`
	ProxySecretRef             string        `json:"proxySecretRef,omitempty"`
	MediatedResolver           string        `json:"mediatedResolver,omitempty"`
	HostFSPosture              string        `json:"hostfsPosture"`
	AdapterPackPosture         string        `json:"adapterPackPosture"`
	Privilege                  PrivilegeFact `json:"privilege"`
	Warnings                   []string      `json:"warnings"`
	NonClaims                  []string      `json:"nonClaims"`
	ProfilePath                string        `json:"profilePath"`
	InitAuditPath              string        `json:"initAuditPath,omitempty"`
	NextSteps                  []string      `json:"nextSteps"`
	HostFSVisibility           string        `json:"hostfsVisibility"`
	HostFSVisibilityRoots      []string      `json:"hostfsVisibilityRoots,omitempty"`
	HostFSVisibilityRuleIds    []string      `json:"hostfsVisibilityRuleIds,omitempty"`
	NameDisclosureAcknowledged bool          `json:"nameDisclosureAcknowledged"`
}

func Catalog() []Template {
	return []Template{
		{
			ID:                 Privacy,
			Recommended:        true,
			Description:        "recommended privacy profile with mediated DNS and proxy routing",
			DefaultNetwork:     "tun2socks",
			RequiresTun2socks:  true,
			EffectivePosture:   "privacy",
			HostFSPosture:      "none-by-default",
			AdapterPackPosture: "none-by-default",
			Warnings: []string{
				"privacy does not claim guest-root containment",
			},
			NonClaims: []string{
				"guest-root containment is not claimed unless privilege separation is enforced",
			},
			FollowUps: []string{
				"hideout doctor --profile <profile> --backend <backend>",
				"hideout profile fs <profile> add --fs read:/absolute/path --reason <reason>",
				"hideout adapter-pack install <source>",
			},
		},
		{
			ID:                 Hardened,
			Description:        "privacy profile plus enforced guest privilege separation",
			DefaultNetwork:     "tun2socks",
			RequiresTun2socks:  true,
			RequiresPrivilege:  PrivilegeEnforced,
			EffectivePosture:   "hardened",
			HostFSPosture:      "none-by-default",
			AdapterPackPosture: "none-by-default",
			Warnings: []string{
				"hardened fails closed unless privilege separation is enforced",
			},
			NonClaims: []string{
				"degraded or unknown privilege status is not a hardened boundary",
			},
			FollowUps: []string{
				"hideout doctor --profile <profile> --backend <backend>",
			},
		},
		{
			ID:                 Dev,
			Description:        "practical development profile with weaker local posture labels",
			DefaultNetwork:     "direct",
			EffectivePosture:   "dev",
			HostFSPosture:      "none-by-default",
			AdapterPackPosture: "none-by-default",
			Warnings: []string{
				"dev is a weaker local development posture",
			},
			NonClaims: []string{
				"dev does not claim network-origin privacy or hardened privilege separation",
			},
			FollowUps: []string{
				"hideout profile fs <profile> add --fs read:/absolute/path --reason <reason>",
			},
		},
		{
			ID:                   Debug,
			Description:          "verbose local-only diagnostics profile",
			DefaultNetwork:       "direct",
			EffectivePosture:     "debug-local",
			HostFSPosture:        "none-by-default",
			AdapterPackPosture:   "none-by-default",
			DevelopmentOnlyLabel: "local-development-only",
			Warnings: []string{
				"debug is local-development-only and may expose more diagnostics",
			},
			NonClaims: []string{
				"debug does not claim privacy or hardened isolation",
			},
			FollowUps: []string{
				"hideout doctor --profile <profile> --backend <backend>",
			},
		},
	}
}

func Lookup(id string) (Template, bool) {
	id = strings.TrimSpace(id)
	for _, candidate := range Catalog() {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Template{}, false
}

func Recommended() Template {
	for _, candidate := range Catalog() {
		if candidate.Recommended {
			return candidate
		}
	}
	return Template{ID: Privacy}
}

func Render(req Request) (Rendered, error) {
	normalized, tmpl, err := normalizeRequest(req)
	if err != nil {
		return Rendered{}, err
	}
	if normalized.CheckExistingProfile && normalized.Store.Root != "" {
		if _, err := os.Stat(normalized.Store.ProfilePath(normalized.ProfileName)); err == nil {
			return Rendered{}, fmt.Errorf("profile %q already exists; recreate/replace is not part of onboarding", normalized.ProfileName)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Rendered{}, err
		}
	}

	p := profile.Default(normalized.ProfileName)
	// Privacy and hardened profiles default the workspace to alias mode so the
	// guest sees /workspace and the host username/path shape is not synthesized
	// into the default workspace path (030). dev/debug keep the preserve default
	// for cross-boundary absolute-path compatibility.
	if tmpl.ID == Privacy || tmpl.ID == Hardened {
		p.Workspace.PathMode = "alias"
	}
	p.Network.Mode = normalized.Network
	p.Network.ProxyEnvVisible = false
	p.Network.ProxySecretRef = normalized.ProxySecretRef
	p.Network.MediatedResolver = normalized.MediatedResolver
	p.Metadata = map[string]string{
		"templateId":                   tmpl.ID,
		"templatePosture":              tmpl.EffectivePosture,
		"templateRecommended":          fmt.Sprintf("%t", tmpl.Recommended),
		"templatePrivilegeStatus":      normalized.Privilege.Status,
		"templatePrivilegeRequirement": tmpl.RequiresPrivilege,
		"templateDegraded":             "false",
		"templateCreatedAt":            normalized.now().Format(time.RFC3339),
	}
	visibilityPosture, visibilityRoots, visibilityRuleIDs, visibilityWarnings, visibilityNonClaims, err := applyVisibilityPreset(&p, normalized)
	if err != nil {
		return Rendered{}, err
	}
	tmpl.HostFSPosture = visibilityPosture
	p.Metadata["hostfsVisibility"] = normalized.VisibilitySelection
	p.Metadata["hostfsVisibilityPosture"] = visibilityPosture
	p.Metadata["hostfsNameDisclosureAcknowledged"] = fmt.Sprintf("%t", normalized.NameDisclosureAcknowledged)

	effective := tmpl.EffectivePosture
	warnings := append([]string(nil), tmpl.Warnings...)
	nonClaims := append([]string(nil), tmpl.NonClaims...)
	warnings = append(warnings, visibilityWarnings...)
	nonClaims = append(nonClaims, visibilityNonClaims...)
	if normalized.AdditionalWarning != "" {
		warnings = append(warnings, normalized.AdditionalWarning)
	}
	if normalized.AdditionalNonClaim != "" {
		nonClaims = append(nonClaims, normalized.AdditionalNonClaim)
	}
	if tmpl.ID == Hardened && normalized.Privilege.Status != PrivilegeEnforced {
		effective = "hardened-degraded"
		p.Metadata["templatePosture"] = effective
		p.Metadata["templateDegraded"] = "true"
		warnings = append(warnings, "hardened was not achieved; degraded fallback was explicitly requested")
		nonClaims = append(nonClaims, "this profile is not a hardened privilege boundary")
	}
	if tmpl.ID != Hardened && normalized.Privilege.Status != PrivilegeEnforced {
		warnings = append(warnings, "privilege separation is "+normalized.Privilege.Status)
	}

	if err := p.Validate(); err != nil {
		return Rendered{}, err
	}

	evidencePath := ""
	profilePath := ""
	if normalized.Store.Root != "" {
		evidencePath = EvidencePath(normalized.Store, normalized.ProfileName)
		profilePath = normalized.Store.ProfilePath(normalized.ProfileName)
	}
	nextSteps := templateCommands(tmpl.FollowUps, normalized)
	evidence := EvidenceSummary{
		Version:                    EvidenceVersion,
		Time:                       normalized.now(),
		Profile:                    normalized.ProfileName,
		Template:                   tmpl.ID,
		EffectivePosture:           effective,
		Backend:                    normalized.Backend,
		Network:                    normalized.Network,
		ProxySecretRef:             normalized.ProxySecretRef,
		MediatedResolver:           normalized.MediatedResolver,
		HostFSPosture:              tmpl.HostFSPosture,
		AdapterPackPosture:         tmpl.AdapterPackPosture,
		Privilege:                  normalized.Privilege,
		Warnings:                   warnings,
		NonClaims:                  nonClaims,
		ProfilePath:                profilePath,
		NextSteps:                  nextSteps,
		HostFSVisibility:           normalized.VisibilitySelection,
		HostFSVisibilityRoots:      append([]string(nil), visibilityRoots...),
		HostFSVisibilityRuleIds:    append([]string(nil), visibilityRuleIDs...),
		NameDisclosureAcknowledged: normalized.NameDisclosureAcknowledged,
	}
	evidence = RedactEvidence(evidence)
	review := Review{Lines: reviewLines(tmpl, normalized, effective, warnings, nonClaims)}
	return Rendered{
		Template:          tmpl,
		Profile:           p,
		EffectivePosture:  effective,
		Warnings:          warnings,
		NonClaims:         nonClaims,
		Review:            review,
		Evidence:          evidence,
		EvidencePath:      evidencePath,
		VisibilityRuleIDs: append([]string(nil), visibilityRuleIDs...),
	}, nil
}

func EvidencePath(store profile.Store, profileName string) string {
	return filepath.Join(store.ProfileDir(profileName), "onboarding-evidence.json")
}

func WriteEvidence(path string, summary EvidenceSummary) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	summary = RedactEvidence(summary)
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func RedactEvidence(in EvidenceSummary) EvidenceSummary {
	in.Profile = audit.RedactString(in.Profile)
	in.Template = audit.RedactString(in.Template)
	in.EffectivePosture = audit.RedactString(in.EffectivePosture)
	in.Backend = audit.RedactString(in.Backend)
	in.Network = audit.RedactString(in.Network)
	in.ProxySecretRef = audit.RedactString(in.ProxySecretRef)
	in.MediatedResolver = audit.RedactString(in.MediatedResolver)
	in.HostFSPosture = audit.RedactString(in.HostFSPosture)
	in.AdapterPackPosture = audit.RedactString(in.AdapterPackPosture)
	in.ProfilePath = audit.RedactString(in.ProfilePath)
	in.InitAuditPath = audit.RedactString(in.InitAuditPath)
	in.Warnings = audit.RedactArgv(in.Warnings)
	in.NonClaims = audit.RedactArgv(in.NonClaims)
	in.NextSteps = audit.RedactArgv(in.NextSteps)
	in.HostFSVisibility = audit.RedactString(in.HostFSVisibility)
	in.HostFSVisibilityRoots = audit.RedactArgv(in.HostFSVisibilityRoots)
	in.HostFSVisibilityRuleIds = audit.RedactArgv(in.HostFSVisibilityRuleIds)
	in.Privilege.Status = audit.RedactString(in.Privilege.Status)
	in.Privilege.Reason = audit.RedactString(in.Privilege.Reason)
	in.Privilege.Guidance = audit.RedactString(in.Privilege.Guidance)
	in.Privilege.Source = audit.RedactString(in.Privilege.Source)
	return in
}

func applyVisibilityPreset(p *profile.Profile, req Request) (string, []string, []string, []string, []string, error) {
	if p == nil {
		return "", nil, nil, nil, nil, errors.New("profile is required for HostFS visibility expansion")
	}
	if req.VisibilitySelection == VisibilityNone {
		return "none-by-default", nil, nil, nil, []string{"host paths outside explicit HostFS rules remain hidden"}, nil
	}
	home := strings.TrimSpace(req.HomeDir)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", nil, nil, nil, nil, err
		}
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", nil, nil, nil, nil, errors.New("HostFS visibility home must be a clean absolute path")
	}
	now := req.now()
	var selected []string
	var ruleIDs []string
	addRule := func(deny bool, root string, scope hostfs.Scope, reason string) {
		rule := hostfs.Rule{
			ID:        visibilityRuleID(deny, root, scope),
			HostPath:  root,
			Ops:       []hostfs.Op{hostfs.OpDiscover},
			Scope:     scope,
			Reason:    reason,
			CreatedAt: &now,
		}
		if deny {
			p.HostFS.Deny = append(p.HostFS.Deny, rule)
		} else {
			p.HostFS.Grants = append(p.HostFS.Grants, rule)
			selected = append(selected, root)
		}
		ruleIDs = append(ruleIDs, rule.ID)
	}
	switch req.VisibilitySelection {
	case VisibilityLandmarks:
		allowed := map[string]bool{}
		for _, name := range []string{"Desktop", "Documents", "Downloads"} {
			allowed[filepath.Join(home, name)] = true
		}
		roots := append([]string(nil), req.VisibilityRoots...)
		if len(roots) == 0 {
			for _, name := range []string{"Desktop", "Documents", "Downloads"} {
				roots = append(roots, filepath.Join(home, name))
			}
		}
		seen := map[string]bool{}
		for _, root := range roots {
			root = filepath.Clean(strings.TrimSpace(root))
			if !allowed[root] {
				return "", nil, nil, nil, nil, fmt.Errorf("landmark root %q must be Desktop, Documents, or Downloads under the selected home", root)
			}
			if seen[root] {
				continue
			}
			seen[root] = true
			addRule(false, root, hostfs.ScopeDir, "confirmed HostFS landmark name visibility")
		}
		return "landmarks-one-level", selected, ruleIDs,
			[]string{"landmark names are user data and may enter target or model context", "accessing protected macOS folders may trigger a TCC prompt"},
			[]string{"landmark visibility does not grant file content or ordinary metadata"}, nil
	case VisibilityHomeTree:
		if len(req.VisibilityRoots) > 0 {
			return "", nil, nil, nil, nil, errors.New("home-tree uses the current home root and does not accept landmark roots")
		}
		addRule(false, home, hostfs.ScopeRecursiveDir, "explicitly acknowledged HostFS home name visibility")
		for _, root := range hostpathrisk.BroadDiscoveryHiddenRootsFor(home, req.Store.Root) {
			if !pathWithinVisibilityRoot(home, root.Path) || root.Path == home {
				continue
			}
			addRule(true, root.Path, hostfs.ScopeRecursiveDir, "broad visibility exclusion: "+string(root.Category))
		}
		return "home-tree-names-visible", selected, ruleIDs,
			[]string{"home-tree discloses non-hidden host names into target or model context", "accessing protected macOS folders may trigger a TCC prompt"},
			[]string{"hidden predictable names may still be inferred from prior knowledge", "home-tree does not grant file content, write authority, or guest-root containment"}, nil
	default:
		return "", nil, nil, nil, nil, fmt.Errorf("unsupported HostFS visibility selection %q", req.VisibilitySelection)
	}
}

func visibilityRuleID(deny bool, root string, scope hostfs.Scope) string {
	effect := "allow"
	if deny {
		effect = "deny"
	}
	sum := sha256.Sum256([]byte(effect + "\x00" + string(scope) + "\x00" + root))
	return "hfs_vis_" + hex.EncodeToString(sum[:6])
}

func pathWithinVisibilityRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func normalizeRequest(req Request) (Request, Template, error) {
	req.ProfileName = strings.TrimSpace(req.ProfileName)
	req.TemplateID = strings.TrimSpace(req.TemplateID)
	req.Backend = strings.TrimSpace(req.Backend)
	req.Network = strings.TrimSpace(req.Network)
	req.ProxySecretRef = strings.TrimSpace(req.ProxySecretRef)
	req.MediatedResolver = strings.TrimSpace(req.MediatedResolver)
	req.Privilege.Status = normalizePrivilegeStatus(req.Privilege.Status)
	req.Privilege.Reason = strings.TrimSpace(req.Privilege.Reason)
	req.Privilege.Guidance = strings.TrimSpace(req.Privilege.Guidance)
	req.Privilege.Source = strings.TrimSpace(req.Privilege.Source)
	req.VisibilitySelection = strings.ToLower(strings.TrimSpace(req.VisibilitySelection))
	if req.VisibilitySelection == "" {
		req.VisibilitySelection = VisibilityNone
	}
	switch req.VisibilitySelection {
	case VisibilityNone, VisibilityLandmarks, VisibilityHomeTree:
	default:
		return req, Template{}, fmt.Errorf("unsupported HostFS visibility selection %q", req.VisibilitySelection)
	}
	if req.VisibilitySelection == VisibilityHomeTree && !req.NameDisclosureAcknowledged {
		return req, Template{}, errors.New("home-tree HostFS visibility requires explicit name-disclosure acknowledgement")
	}
	if req.VisibilitySelection == VisibilityNone && len(req.VisibilityRoots) > 0 {
		return req, Template{}, errors.New("HostFS visibility roots require landmarks selection")
	}
	if req.Privilege.Status == "" {
		req.Privilege.Status = PrivilegeUnknown
	}
	if req.Privilege.Source == "" {
		req.Privilege.Source = "operator-declared"
	}
	if req.ProfileName == "" {
		req.ProfileName = "default"
	}
	if err := profile.ValidateName(req.ProfileName); err != nil {
		return req, Template{}, err
	}
	if req.NoInput {
		var missing []string
		if !req.ExplicitProfile {
			missing = append(missing, "--profile")
		}
		if !req.ExplicitTemplate {
			missing = append(missing, "--template")
		}
		if !req.ExplicitBackend {
			missing = append(missing, "--backend")
		}
		if !req.ExplicitNetwork {
			missing = append(missing, "--network")
		}
		if len(missing) > 0 {
			return req, Template{}, fmt.Errorf("non-interactive onboarding requires explicit choices: %s", strings.Join(missing, ", "))
		}
	}
	if req.TemplateID == "" {
		if req.NoInput {
			return req, Template{}, errors.New("non-interactive onboarding requires --template")
		}
		req.TemplateID = Recommended().ID
	}
	tmpl, ok := Lookup(req.TemplateID)
	if !ok {
		return req, Template{}, fmt.Errorf("unsupported profile template %q", req.TemplateID)
	}
	if req.Backend == "" {
		if req.NoInput {
			return req, Template{}, errors.New("non-interactive onboarding requires --backend")
		}
		req.Backend = "lima"
	}
	if req.Network == "" {
		if req.NoInput {
			return req, Template{}, errors.New("non-interactive onboarding requires --network")
		}
		req.Network = tmpl.DefaultNetwork
	}
	switch req.Backend {
	case "native", "lima":
	default:
		return req, Template{}, fmt.Errorf("backend %q is not implemented yet", req.Backend)
	}
	switch req.Network {
	case "direct", "tun2socks":
	default:
		return req, Template{}, fmt.Errorf("unsupported network mode %q", req.Network)
	}
	if tmpl.RequiresTun2socks && req.Network != "tun2socks" {
		return req, Template{}, fmt.Errorf("template %s requires --network tun2socks", tmpl.ID)
	}
	switch req.Network {
	case "direct":
		if req.ProxySecretRef != "" {
			return req, Template{}, errors.New("direct network mode does not use a proxy secret ref")
		}
		if req.MediatedResolver != "" {
			return req, Template{}, errors.New("direct network mode does not use a mediated resolver")
		}
	case "tun2socks":
		if req.ProxySecretRef == "" {
			return req, Template{}, errors.New("tun2socks network mode requires a proxy secret ref")
		}
		if err := secrets.ValidateRef(req.ProxySecretRef); err != nil {
			return req, Template{}, fmt.Errorf("proxy secret ref: %w", err)
		}
		if req.MediatedResolver == "" {
			return req, Template{}, errors.New("tun2socks network mode requires a mediated resolver")
		}
		if net.ParseIP(req.MediatedResolver) == nil {
			return req, Template{}, fmt.Errorf("mediated resolver %q is not an IP address", req.MediatedResolver)
		}
	}
	if tmpl.ID == Hardened && req.Privilege.Status != PrivilegeEnforced && !req.AllowDegradedTemplate {
		return req, Template{}, fmt.Errorf("hardened requires enforced privilege separation; status=%s; recreate with a no-sudo base image or pass --allow-degraded-template to create a visibly degraded profile", req.Privilege.Status)
	}
	if tmpl.ID == Hardened && req.Backend == "native" && req.Privilege.Status == PrivilegeEnforced {
		return req, Template{}, errors.New("hardened cannot claim enforced privilege separation on the native backend; use a Lima profile with observed privilege separation or pass --allow-degraded-template with degraded/unknown status")
	}
	return req, tmpl, nil
}

func normalizePrivilegeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", PrivilegeUnknown:
		return PrivilegeUnknown
	case PrivilegeEnforced:
		return PrivilegeEnforced
	case PrivilegeDegraded:
		return PrivilegeDegraded
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (req Request) now() time.Time {
	if !req.Now.IsZero() {
		return req.Now.UTC()
	}
	return time.Now().UTC()
}

func templateCommands(values []string, req Request) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ReplaceAll(value, "<profile>", req.ProfileName)
		value = strings.ReplaceAll(value, "<backend>", req.Backend)
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func reviewLines(t Template, req Request, effective string, warnings, nonClaims []string) []string {
	lines := []string{
		"recommended template: " + Recommended().ID,
		"selected template: " + t.ID,
		"effective posture: " + effective,
		"backend: " + req.Backend,
		"network: " + req.Network,
		"HostFS: " + t.HostFSPosture,
		"HostFS visibility: " + req.VisibilitySelection,
		"adapter packs: " + t.AdapterPackPosture,
		"privilege: " + req.Privilege.Status,
	}
	if req.ProxySecretRef != "" {
		lines = append(lines, "proxy secret ref: "+audit.RedactString(req.ProxySecretRef))
	}
	if req.MediatedResolver != "" {
		lines = append(lines, "mediated resolver: "+audit.RedactString(req.MediatedResolver))
	}
	for _, warning := range warnings {
		lines = append(lines, "warning: "+audit.RedactString(warning))
	}
	for _, nonClaim := range nonClaims {
		lines = append(lines, "non-claim: "+audit.RedactString(nonClaim))
	}
	return lines
}
