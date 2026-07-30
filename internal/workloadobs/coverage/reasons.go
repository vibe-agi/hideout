package coverage

import (
	"sort"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	ReasonObserverReady        = "observer-ready"
	ReasonCollectorRecovered   = "collector-recovered"
	ReasonObserverStarting     = "observer-starting"
	ReasonObserverRestarted    = "observer-restarted"
	ReasonCollectorPartial     = "collector-partial"
	ReasonCollectorUnavailable = "collector-unavailable"
	ReasonBackendUnsupported   = "backend-unsupported"
	ReasonCgroupUnavailable    = "cgroup-unavailable"
	ReasonHookUnavailable      = "hook-unavailable"
	ReasonBPFUnavailable       = "bpf-unavailable"
	ReasonFanotifyUnavailable  = "fanotify-unavailable"
	ReasonPermissionDenied     = "permission-denied"
	ReasonCollectorRestarted   = "collector-restarted"
	ReasonSequenceGap          = "sequence-gap"
	ReasonRingOverflow         = "ring-overflow"
	ReasonTransportDrop        = "transport-drop"
	ReasonInvalidFrame         = "invalid-frame"
	ReasonSchemaMismatch       = "schema-mismatch"
	ReasonPathUnresolved       = "path-unresolved"
	ReasonActorUnresolved      = "actor-unresolved"
	ReasonEncryptedDNS         = "encrypted-dns"
	ReasonRedactionDropped     = "redaction-dropped"
	ReasonRetentionPruned      = "retention-pruned"
	ReasonQuotaPruned          = "quota-pruned"
	ReasonStoreCorrupt         = "store-corrupt"
	ReasonOwnerCleaned         = "owner-cleaned"
	ReasonDaemonDisconnected   = "daemon-disconnected"
	ReasonCleanupUnproved      = "cleanup-unproved"
	ReasonTargetExited         = "target-exited"
	ReasonTargetTamper         = "target-tamper"
)

type ReasonDefinition struct {
	Code                 string
	AllowedStates        []string
	Loss                 bool
	RequiresLossEvidence bool
	Description          string
}

var reasonRegistry = map[string]ReasonDefinition{
	ReasonObserverReady: {
		Code: ReasonObserverReady, AllowedStates: []string{workloadtypes.CoverageAvailable},
		Description: "The collector is ready and no loss is known for this interval.",
	},
	ReasonCollectorRecovered: {
		Code: ReasonCollectorRecovered, AllowedStates: []string{workloadtypes.CoverageAvailable},
		Description: "A fresh interval began after an explicitly closed degraded interval.",
	},
	ReasonObserverStarting: {
		Code: ReasonObserverStarting, AllowedStates: []string{workloadtypes.CoveragePartial},
		Description: "The workload boundary exists but the collector is not fully ready.",
	},
	ReasonObserverRestarted: {
		Code: ReasonObserverRestarted, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, Description: "Observer generation changed and continuity across the restart is not proved.",
	},
	ReasonCollectorPartial: {
		Code: ReasonCollectorPartial, AllowedStates: []string{workloadtypes.CoveragePartial},
		Description: "The collector reported an explicitly limited capability.",
	},
	ReasonCollectorUnavailable: {
		Code: ReasonCollectorUnavailable, AllowedStates: []string{workloadtypes.CoverageUnavailable},
		Description: "The collector reported that this subsystem is unavailable.",
	},
	ReasonBackendUnsupported: {
		Code: ReasonBackendUnsupported, AllowedStates: []string{workloadtypes.CoverageUnavailable},
		Description: "The selected backend has no supported collector for this subsystem.",
	},
	ReasonCgroupUnavailable: {
		Code: ReasonCgroupUnavailable, AllowedStates: []string{workloadtypes.CoverageUnavailable},
		Description: "The workload cgroup could not be established or proved.",
	},
	ReasonHookUnavailable: {
		Code: ReasonHookUnavailable, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Description: "A required observation hook became unavailable.",
	},
	ReasonBPFUnavailable: {
		Code: ReasonBPFUnavailable, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Description: "The required eBPF hook is unavailable.",
	},
	ReasonFanotifyUnavailable: {
		Code: ReasonFanotifyUnavailable, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Description: "The required fanotify provider is unavailable.",
	},
	ReasonPermissionDenied: {
		Code: ReasonPermissionDenied, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Description: "The collector lacks a required guest permission.",
	},
	ReasonCollectorRestarted: {
		Code: ReasonCollectorRestarted, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, Description: "Collector generation changed and continuity is not proved.",
	},
	ReasonSequenceGap: {
		Code: ReasonSequenceGap, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, RequiresLossEvidence: true,
		Description: "One or more observer sequence numbers were not received.",
	},
	ReasonRingOverflow: {
		Code: ReasonRingOverflow, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, RequiresLossEvidence: true,
		Description: "The kernel or user-space event ring overflowed.",
	},
	ReasonTransportDrop: {
		Code: ReasonTransportDrop, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, RequiresLossEvidence: true,
		Description: "The authenticated observer transport reported a drop.",
	},
	ReasonInvalidFrame: {
		Code: ReasonInvalidFrame, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, RequiresLossEvidence: true,
		Description: "An invalid observer frame was rejected and cannot be reconstructed.",
	},
	ReasonSchemaMismatch: {
		Code: ReasonSchemaMismatch, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, RequiresLossEvidence: true,
		Description: "An unsupported observer schema was rejected and cannot be reconstructed.",
	},
	ReasonPathUnresolved: {
		Code: ReasonPathUnresolved, AllowedStates: []string{workloadtypes.CoveragePartial},
		Description: "A file event was observed but its stable path could not be proved.",
	},
	ReasonActorUnresolved: {
		Code: ReasonActorUnresolved, AllowedStates: []string{workloadtypes.CoveragePartial},
		Description: "An event was observed but its originating workload process could not be proved.",
	},
	ReasonEncryptedDNS: {
		Code: ReasonEncryptedDNS, AllowedStates: []string{workloadtypes.CoveragePartial},
		Description: "Name-resolution intent is hidden inside an encrypted DNS transport.",
	},
	ReasonRedactionDropped: {
		Code: ReasonRedactionDropped, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, RequiresLossEvidence: true,
		Description: "An activity record was dropped because pre-persistence redaction could not be proved.",
	},
	ReasonRetentionPruned: {
		Code: ReasonRetentionPruned, AllowedStates: []string{workloadtypes.CoveragePartial},
		Loss: true, Description: "Historical activity was removed by the retention lifecycle.",
	},
	ReasonQuotaPruned: {
		Code: ReasonQuotaPruned, AllowedStates: []string{workloadtypes.CoveragePartial},
		Loss: true, Description: "Historical activity was removed to enforce the storage quota.",
	},
	ReasonStoreCorrupt: {
		Code: ReasonStoreCorrupt, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, Description: "Stored evidence failed strict validation.",
	},
	ReasonOwnerCleaned: {
		Code: ReasonOwnerCleaned, AllowedStates: []string{workloadtypes.CoverageUnavailable},
		Description: "The exact activity owner completed lifecycle cleanup.",
	},
	ReasonDaemonDisconnected: {
		Code: ReasonDaemonDisconnected, AllowedStates: []string{workloadtypes.CoverageUnavailable},
		Loss: true, Description: "The daemon lost the authenticated observer stream and continuity is not proved.",
	},
	ReasonCleanupUnproved: {
		Code: ReasonCleanupUnproved, AllowedStates: []string{workloadtypes.CoverageUnavailable},
		Description: "The observer or workload boundary cleanup could not be proved.",
	},
	ReasonTargetExited: {
		Code: ReasonTargetExited, AllowedStates: []string{workloadtypes.CoverageUnavailable},
		Description: "The observed target and its workload boundary exited.",
	},
	ReasonTargetTamper: {
		Code: ReasonTargetTamper, AllowedStates: []string{workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable},
		Loss: true, Description: "The target disturbed an observation prerequisite.",
	},
}

func Reason(code string) (ReasonDefinition, bool) {
	definition, ok := reasonRegistry[code]
	if !ok {
		return ReasonDefinition{}, false
	}
	definition.AllowedStates = append([]string(nil), definition.AllowedStates...)
	return definition, true
}

func Reasons() []ReasonDefinition {
	codes := make([]string, 0, len(reasonRegistry))
	for code := range reasonRegistry {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	definitions := make([]ReasonDefinition, 0, len(codes))
	for _, code := range codes {
		definition, _ := Reason(code)
		definitions = append(definitions, definition)
	}
	return definitions
}
