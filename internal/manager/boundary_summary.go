package manager

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
)

const BoundarySummaryVersion = "hideout.boundary-summary/v1"

type BoundarySummary struct {
	Version          string                      `json:"version"`
	Evidence         string                      `json:"evidence"`
	AuditPath        string                      `json:"auditPath,omitempty"`
	Privilege        *BoundaryPrivilegeSummary   `json:"privilege,omitempty"`
	HostFSVisibility *HostFSVisibilityPosture    `json:"hostfsVisibility,omitempty"`
	Runtime          *BoundaryRuntimeSummary     `json:"runtime,omitempty"`
	Capabilities     []BoundaryCapabilitySummary `json:"capabilities"`
}

type BoundaryRuntimeSummary struct {
	Family          string `json:"family"`
	Revision        string `json:"revision"`
	Status          string `json:"status"`
	ContractDigest  string `json:"contractDigest"`
	ArtifactDigest  string `json:"artifactDigest"`
	PrivilegeStatus string `json:"privilegeStatus"`
	FailedCount     int    `json:"failedCount"`
	RecoveryCode    string `json:"recoveryCode,omitempty"`
}

type BoundaryPrivilegeSummary struct {
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Guidance  string `json:"guidance,omitempty"`
	TargetUID string `json:"targetUid,omitempty"`
	SetupKind string `json:"setupKind,omitempty"`
	NonClaim  string `json:"nonClaim,omitempty"`
}

type BoundaryCapabilitySummary struct {
	Capability       string `json:"capability"`
	Owner            string `json:"owner,omitempty"`
	Source           string `json:"source,omitempty"`
	Lifetime         string `json:"lifetime,omitempty"`
	CloseReason      string `json:"closeReason,omitempty"`
	EndpointCategory string `json:"endpointCategory,omitempty"`
	Allowed          int    `json:"allowed"`
	Denied           int    `json:"denied"`
	Unsupported      int    `json:"unsupported,omitempty"`
	Error            int    `json:"error,omitempty"`
	AuditOnly        int    `json:"auditOnly,omitempty"`
	PackID           string `json:"packId,omitempty"`
	RevisionID       string `json:"revisionId,omitempty"`
	BindingID        string `json:"bindingId,omitempty"`
	QualifiedAppRef  string `json:"qualifiedAppRef,omitempty"`
	Access           string `json:"access,omitempty"`
	ResourceKind     string `json:"resourceKind,omitempty"`
	LastOutcome      string `json:"lastOutcome,omitempty"`
	RecoveryCode     string `json:"recoveryCode,omitempty"`
}

func SummarizeRunBoundary(auditPath string) BoundarySummary {
	summary := newBoundarySummary(auditPath)
	if auditPath == "" || auditPath == "off" {
		summary.evidence = "disabled"
		return summary.snapshot()
	}
	file, err := os.Open(auditPath)
	if err != nil {
		summary.evidence = "unavailable"
		return summary.snapshot()
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		summary.observe(event)
	}
	return summary.snapshot()
}

type boundarySummaryBuilder struct {
	auditPath        string
	evidence         string
	privilege        *BoundaryPrivilegeSummary
	hostFSVisibility *HostFSVisibilityPosture
	runtime          *BoundaryRuntimeSummary
	capabilities     map[string]*BoundaryCapabilitySummary
}

func newBoundarySummary(auditPath string) *boundarySummaryBuilder {
	builder := &boundarySummaryBuilder{
		auditPath:    auditPath,
		evidence:     "available",
		capabilities: map[string]*BoundaryCapabilitySummary{},
	}
	builder.capability("hostfs")
	builder.capability("host.open")
	builder.capability("command.adapter")
	builder.capability("host.app.open-resource")
	return builder
}

func (b *boundarySummaryBuilder) observe(event audit.Event) {
	if event.Action == "session.start" {
		if posture := stringDetail(event.Details, "hostfsVisibilityPosture"); posture != "" {
			b.hostFSVisibility = &HostFSVisibilityPosture{
				Posture:        posture,
				DiscoverGrants: detailInt(event.Details, "hostfsDiscoverGrants"),
				DiscoverDeny:   detailInt(event.Details, "hostfsDiscoverDeny"),
				MaxListEntries: detailInt(event.Details, "hostfsMaxListEntries"),
				MaxDepth:       detailInt(event.Details, "hostfsMaxDepth"),
				NonClaim:       stringDetail(event.Details, "hostfsVisibilityNonClaim"),
			}
		}
	}
	if event.Action == "guest.privilege.status" {
		b.privilege = boundaryPrivilege(event.Details)
		return
	}
	if event.Action == "runtime.verify" {
		b.runtime = &BoundaryRuntimeSummary{
			Family: stringDetail(event.Details, "family"), Revision: stringDetail(event.Details, "revision"),
			Status: stringDetail(event.Details, "status"), ContractDigest: stringDetail(event.Details, "contractDigest"),
			ArtifactDigest: stringDetail(event.Details, "artifactDigest"), PrivilegeStatus: stringDetail(event.Details, "privilegeStatus"),
			FailedCount: detailInt(event.Details, "failedCount"), RecoveryCode: stringDetail(event.Details, "recoveryCode"),
		}
		return
	}
	capability := boundaryCapability(event.Action)
	if capability == "" {
		return
	}
	item := b.capability(capability)
	switch boundaryDecision(event) {
	case "allow":
		item.Allowed++
	case "deny":
		item.Denied++
	case "unsupported":
		item.Unsupported++
	case "error":
		item.Error++
	case "audit-only":
		item.AuditOnly++
	}
	if item.Owner == "" {
		item.Owner = stringDetail(event.Details, "owner")
	}
	if item.Source == "" {
		item.Source = stringDetail(event.Details, "source")
	}
	if item.Lifetime == "" {
		item.Lifetime = stringDetail(event.Details, "lifetime")
	}
	if item.CloseReason == "" {
		item.CloseReason = stringDetail(event.Details, "closeReason")
	}
	if item.EndpointCategory == "" {
		item.EndpointCategory = stringDetail(event.Details, "endpointCategory")
	}
	for target, key := range map[*string]string{
		&item.PackID:          "packId",
		&item.RevisionID:      "revisionId",
		&item.BindingID:       "bindingId",
		&item.QualifiedAppRef: "qualifiedAppRef",
		&item.Access:          "access",
		&item.ResourceKind:    "resourceKind",
		&item.LastOutcome:     "outcome",
		&item.RecoveryCode:    "recoveryCode",
	} {
		if value := stringDetail(event.Details, key); value != "" {
			*target = value
		}
	}
}

func (b *boundarySummaryBuilder) capability(name string) *BoundaryCapabilitySummary {
	item, ok := b.capabilities[name]
	if ok {
		return item
	}
	item = &BoundaryCapabilitySummary{Capability: name}
	b.capabilities[name] = item
	return item
}

func (b *boundarySummaryBuilder) snapshot() BoundarySummary {
	capabilities := make([]BoundaryCapabilitySummary, 0, len(b.capabilities))
	for _, item := range b.capabilities {
		capabilities = append(capabilities, *item)
	}
	sort.Slice(capabilities, func(i, j int) bool {
		return capabilities[i].Capability < capabilities[j].Capability
	})
	return BoundarySummary{
		Version:          BoundarySummaryVersion,
		Evidence:         b.evidence,
		AuditPath:        b.auditPath,
		Privilege:        b.privilege,
		HostFSVisibility: b.hostFSVisibility,
		Runtime:          b.runtime,
		Capabilities:     capabilities,
	}
}

func detailInt(details map[string]any, key string) int {
	value := detailString(details, key)
	out, _ := strconv.Atoi(value)
	return out
}

func boundaryPrivilege(details map[string]any) *BoundaryPrivilegeSummary {
	if details == nil {
		return nil
	}
	status := stringDetail(details, "status")
	if status == "" {
		return nil
	}
	return &BoundaryPrivilegeSummary{
		Status:    status,
		Reason:    stringDetail(details, "reason"),
		Guidance:  stringDetail(details, "guidance"),
		TargetUID: detailString(details, "target.uid"),
		SetupKind: stringDetail(details, "setup.kind"),
		NonClaim:  stringDetail(details, "nonClaim"),
	}
}

func boundaryCapability(action string) string {
	switch {
	case strings.HasPrefix(action, "host.fs."):
		return "hostfs"
	case action == "host.open":
		return "host.open"
	case action == "command.adapter":
		return "command.adapter"
	case action == "target.root_attempt":
		return "command.adapter"
	case action == "preview.open":
		return "preview.open"
	case strings.HasPrefix(action, "host.app."):
		return "host.app.open-resource"
	case strings.HasPrefix(action, "endpoint.expose."):
		return action
	case strings.HasPrefix(action, "portbridge."):
		return action
	default:
		return ""
	}
}

func boundaryDecision(event audit.Event) string {
	if strings.HasPrefix(event.Action, "host.fs.") {
		return hostFSBoundaryDecision(event)
	}
	if status := stringDetail(event.Details, "status"); status == "error" {
		return "error"
	}
	switch event.Decision {
	case "allow", "deny", "error", "audit-only":
		return event.Decision
	default:
		return ""
	}
}

func hostFSBoundaryDecision(event audit.Event) string {
	if status := stringDetail(event.Details, "status"); status == "error" {
		return "error"
	}
	switch stringDetail(event.Details, "policyEffect") {
	case "allow":
		return "allow"
	case "deny", "none":
		return "deny"
	case "unsupported":
		return "unsupported"
	}
	switch event.Decision {
	case "allow", "deny", "error", "audit-only":
		return event.Decision
	default:
		return ""
	}
}

func stringDetail(details map[string]any, key string) string {
	value, ok := details[key]
	if !ok {
		return ""
	}
	out, ok := value.(string)
	if !ok {
		return ""
	}
	return out
}

func detailString(details map[string]any, key string) string {
	value, ok := details[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strings.TrimSuffix(strings.TrimSuffix(strconv.FormatFloat(v, 'f', 3, 64), "0"), ".")
	case int:
		return strconv.Itoa(v)
	default:
		return ""
	}
}
