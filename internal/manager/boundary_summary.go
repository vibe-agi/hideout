package manager

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
)

const BoundarySummaryVersion = "hideout.boundary-summary/v1"

type BoundarySummary struct {
	Version      string                      `json:"version"`
	AuditPath    string                      `json:"auditPath,omitempty"`
	Capabilities []BoundaryCapabilitySummary `json:"capabilities"`
}

type BoundaryCapabilitySummary struct {
	Capability       string `json:"capability"`
	Owner            string `json:"owner,omitempty"`
	Lifetime         string `json:"lifetime,omitempty"`
	EndpointCategory string `json:"endpointCategory,omitempty"`
	Allowed          int    `json:"allowed"`
	Denied           int    `json:"denied"`
	Unsupported      int    `json:"unsupported,omitempty"`
	Error            int    `json:"error,omitempty"`
	AuditOnly        int    `json:"auditOnly,omitempty"`
}

func SummarizeRunBoundary(auditPath string) BoundarySummary {
	summary := newBoundarySummary(auditPath)
	if auditPath == "" || auditPath == "off" {
		return summary.snapshot()
	}
	file, err := os.Open(auditPath)
	if err != nil {
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
	auditPath    string
	capabilities map[string]*BoundaryCapabilitySummary
}

func newBoundarySummary(auditPath string) *boundarySummaryBuilder {
	builder := &boundarySummaryBuilder{
		auditPath:    auditPath,
		capabilities: map[string]*BoundaryCapabilitySummary{},
	}
	builder.capability("hostfs")
	builder.capability("host.open")
	return builder
}

func (b *boundarySummaryBuilder) observe(event audit.Event) {
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
	if item.Lifetime == "" {
		item.Lifetime = stringDetail(event.Details, "lifetime")
	}
	if item.EndpointCategory == "" {
		item.EndpointCategory = stringDetail(event.Details, "endpointCategory")
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
		Version:      BoundarySummaryVersion,
		AuditPath:    b.auditPath,
		Capabilities: capabilities,
	}
}

func boundaryCapability(action string) string {
	switch {
	case strings.HasPrefix(action, "host.fs."):
		return "hostfs"
	case action == "host.open":
		return "host.open"
	case action == "preview.open":
		return "preview.open"
	case strings.HasPrefix(action, "portbridge."):
		return action
	default:
		return ""
	}
}

func boundaryDecision(event audit.Event) string {
	if effect := stringDetail(event.Details, "policyEffect"); effect == "unsupported" {
		return "unsupported"
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
