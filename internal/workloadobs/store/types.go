// Package store persists already-redacted workload activity in bounded,
// exact-owner, checksummed segments on the host.
package store

import (
	"errors"
	"sync"
	"time"

	workloadobs "github.com/vibe-agi/hideout/internal/workloadobs"
	workloadcoverage "github.com/vibe-agi/hideout/internal/workloadobs/coverage"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	DefaultActiveSegmentBytes = workloadobs.DefaultActivityActiveSegmentBytes
	DefaultPerOwnerBytes      = workloadobs.DefaultActivityPerOwnerBytes
	DefaultGlobalBytes        = workloadobs.DefaultActivityGlobalBytes
	MaxAppendBatchEntries     = 256

	ownersDirectory     = "owners"
	sealedDirectory     = "sealed"
	indexDirectory      = "indexes"
	quarantineDirectory = "quarantine"
	pruningDirectory    = "pruning"
	deletingDirectory   = "deleting"

	ownerMetadataFile = "owner.json"
	ownerStateFile    = "state.json"
	activeSegmentFile = "active.seg"

	segmentSuffix  = ".seg"
	manifestSuffix = ".manifest.json"
	indexSuffix    = ".index.json"

	ownerMetadataSchema = "hideout.activity-owner-metadata.v1"
	ownerStateSchema    = "hideout.activity-owner-state.v1"
	segmentSchema       = "hideout.activity-segment.v1"
	segmentEntrySchema  = "hideout.activity-segment-entry.v1"
	indexSchema         = "hideout.activity-segment-index.v1"

	RetentionReasonPruned  = "retention-pruned"
	RetentionReasonCorrupt = "corrupt-segment"

	CoverageReasonRetentionPruned = workloadcoverage.ReasonRetentionPruned
	CoverageReasonStoreCorrupt    = workloadcoverage.ReasonStoreCorrupt

	RepairTornWrite             = "torn-write"
	RepairCRCFailure            = "crc-failure"
	RepairInvalidFrame          = "invalid-frame"
	RepairIndexRebuilt          = "index-rebuilt"
	RepairOrphanSealedRecovered = "orphan-sealed-recovered"
	RepairSealedQuarantined     = "sealed-quarantined"

	maxSegmentBytes      = 64 << 20
	maxStoreBytes        = 10 << 30
	maxStateGaps         = 4096
	maxSegmentScopes     = 4096
	maxIndexEntries      = 1 << 20
	maxIndexEncodedBytes = 32 << 20
)

var (
	ErrInvalidOptions         = errors.New("activity store options are invalid")
	ErrClosed                 = errors.New("activity store is closed")
	ErrRecordNotPersistable   = errors.New("activity record is not persistable")
	ErrFrameTooLarge          = errors.New("activity frame exceeds the active segment bound")
	ErrEmptySegment           = errors.New("activity segment is empty")
	ErrOwnerNotFound          = errors.New("activity owner was not found")
	ErrOwnerRetentionConflict = errors.New(
		"activity owner retention policy is already bound",
	)
	ErrInsecurePath = errors.New("activity store path is not private and exact")
	ErrStoreCorrupt = errors.New("activity store metadata is corrupt")
)

type Options struct {
	Root               string
	ActiveSegmentBytes int64
	PerOwnerBytes      int64
	GlobalBytes        int64
	Now                func() time.Time
}

type Store struct {
	mu      sync.Mutex
	options Options
	root    string
	repairs []Repair
	closed  bool
}

type Repair struct {
	Kind           string `json:"kind"`
	OwnerKey       string `json:"ownerKey"`
	SegmentID      string `json:"segmentId,omitempty"`
	ValidRecords   uint64 `json:"validRecords"`
	DiscardedBytes int64  `json:"discardedBytes"`
}

type SegmentManifest struct {
	Schema      string                      `json:"schema"`
	ID          string                      `json:"id"`
	Owner       workloadtypes.ActivityOwner `json:"owner"`
	FirstSeq    uint64                      `json:"firstSeq,omitempty"`
	LastSeq     uint64                      `json:"lastSeq,omitempty"`
	FirstAt     time.Time                   `json:"firstAt"`
	LastAt      time.Time                   `json:"lastAt"`
	Bytes       uint64                      `json:"bytes"`
	Records     uint64                      `json:"records"`
	State       string                      `json:"state"`
	SHA256      string                      `json:"sha256"`
	IndexDigest string                      `json:"indexDigest"`
	CreatedAt   time.Time                   `json:"createdAt"`
	SealedAt    time.Time                   `json:"sealedAt"`
	Scopes      []segmentScope              `json:"scopes"`
}

type StoreStats struct {
	UsedBytes              uint64 `json:"usedBytes"`
	LimitBytes             uint64 `json:"limitBytes"`
	DefaultOwnerLimitBytes uint64 `json:"defaultOwnerLimitBytes"`
	ActiveSegmentBytes     uint64 `json:"activeSegmentBytes"`
	Owners                 int    `json:"owners"`
	Segments               int    `json:"segments"`
}

type OwnerStats struct {
	Owner         workloadtypes.ActivityOwner `json:"owner"`
	UsedBytes     uint64                      `json:"usedBytes"`
	LimitBytes    uint64                      `json:"limitBytes"`
	MaxAgeSeconds int64                       `json:"maxAgeSeconds"`
	Segments      int                         `json:"segments"`
}

type DeletionProof struct {
	Schema        string                      `json:"schema"`
	Owner         workloadtypes.ActivityOwner `json:"owner"`
	OwnerKey      string                      `json:"ownerKey"`
	Status        string                      `json:"status"`
	AlreadyAbsent bool                        `json:"alreadyAbsent"`
	RemovedBytes  uint64                      `json:"removedBytes"`
	RemovedFiles  uint64                      `json:"removedFiles"`
	ObservedAt    time.Time                   `json:"observedAt"`
}

const (
	deletionProofSchema = "hideout.activity-owner-deletion-proof.v1"
	DeletionAbsent      = "absent"
)

func (proof DeletionProof) Validate() error {
	if proof.Schema != deletionProofSchema ||
		proof.Owner.Validate() != nil ||
		proof.OwnerKey != proof.Owner.Key() ||
		proof.Status != DeletionAbsent ||
		proof.ObservedAt.IsZero() {
		return ErrStoreCorrupt
	}
	if proof.AlreadyAbsent &&
		(proof.RemovedBytes != 0 || proof.RemovedFiles != 0) {
		return ErrStoreCorrupt
	}
	return nil
}

var _ workloadquery.Source = (*Store)(nil)

type ownerMetadata struct {
	Schema    string                                 `json:"schema"`
	Owner     workloadtypes.ActivityOwner            `json:"owner"`
	CreatedAt time.Time                              `json:"createdAt"`
	Retention *workloadtypes.ActivityRetentionPolicy `json:"retention,omitempty"`
}

type ownerPersistentState struct {
	Schema          string                           `json:"schema"`
	OwnerKey        string                           `json:"ownerKey"`
	Pruned          bool                             `json:"pruned"`
	Corrupt         bool                             `json:"corrupt"`
	Reasons         []string                         `json:"reasons"`
	Gaps            []workloadtypes.CoverageInterval `json:"gaps"`
	PendingPruneIDs []string                         `json:"pendingPruneIds"`
}

type segmentScope struct {
	SessionID           string    `json:"sessionId"`
	Subsystem           string    `json:"subsystem"`
	CollectorGeneration uint64    `json:"collectorGeneration"`
	FirstSequence       uint64    `json:"firstSequence,omitempty"`
	LastSequence        uint64    `json:"lastSequence,omitempty"`
	FirstAt             time.Time `json:"firstAt"`
	LastAt              time.Time `json:"lastAt"`
}
