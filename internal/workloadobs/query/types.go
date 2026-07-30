// Package query provides deterministic, exact-owner read models over already
// redacted workload activity. It does not own persistence or owner resolution.
package query

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	DefaultLimit = 100
	MaximumLimit = 500

	DefaultHighestRiskLimit = 10
)

var (
	ErrInvalidOptions       = errors.New("activity query options are invalid")
	ErrInvalidSnapshot      = errors.New("activity query snapshot is invalid")
	ErrInvalidQuery         = errors.New("activity query is invalid")
	ErrOwnerNotFound        = errors.New("activity owner was not found")
	ErrCursorInvalid        = errors.New("activity cursor is invalid")
	ErrCursorOwnerMismatch  = errors.New("activity cursor belongs to another owner")
	ErrCursorFilterMismatch = errors.New("activity cursor belongs to different filters")
	ErrCursorStale          = errors.New("activity cursor refers to a different snapshot")
	ErrExecutionNotFound    = errors.New("activity execution was not found")

	revisionPattern  = regexp.MustCompile(`^rev_[A-Za-z0-9_-]{8,124}$`)
	sessionPattern   = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{1,124}$`)
	operationPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	executionPattern = regexp.MustCompile(`^exec_[A-Za-z0-9_-]{8,124}$`)
	riskPattern      = regexp.MustCompile(`^(risk_[A-Za-z0-9_-]{8,124}|[a-z][a-z0-9.-]{2,127})$`)
)

type Source interface {
	Snapshot(
		ctx context.Context,
		owner workloadtypes.ActivityOwner,
	) (Snapshot, error)
}

type Options struct {
	Source    Source
	CursorKey []byte
}

type Snapshot struct {
	Revision   string                      `json:"revision"`
	Owner      workloadtypes.ActivityOwner `json:"owner"`
	Records    []workloadtypes.ActivityRecord
	Executions []workloadtypes.Execution
	Coverage   []workloadtypes.CoverageInterval
	Risks      []risk.Finding
	Retention  RetentionState
}

type RetentionState struct {
	EarliestAt    time.Time `json:"earliestAt,omitempty"`
	LatestAt      time.Time `json:"latestAt,omitempty"`
	UsedBytes     uint64    `json:"usedBytes"`
	LimitBytes    uint64    `json:"limitBytes"`
	MaxAgeSeconds int64     `json:"maxAgeSeconds"`
	Pruned        bool      `json:"pruned"`
	Corrupt       bool      `json:"corrupt"`
	Reasons       []string  `json:"reasons"`
}

type TimeRange struct {
	From time.Time `json:"from,omitempty"`
	To   time.Time `json:"to,omitempty"`
}

type QuotaSummary struct {
	UsedBytes     uint64 `json:"usedBytes"`
	LimitBytes    uint64 `json:"limitBytes"`
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
}

type SummaryQuery struct {
	Owner     workloadtypes.ActivityOwner
	SessionID string
	From      time.Time
	To        time.Time
}

type SummaryResult struct {
	Owner           workloadtypes.ActivityOwner      `json:"owner"`
	Counts          map[string]uint64                `json:"counts"`
	CurrentCoverage []workloadtypes.CoverageInterval `json:"currentCoverage"`
	HighestRisks    []risk.Finding                   `json:"highestRisks"`
	RetainedRange   TimeRange                        `json:"retainedRange"`
	Quota           QuotaSummary                     `json:"quota"`
	Pruned          bool                             `json:"pruned"`
	Corrupt         bool                             `json:"corrupt"`
	Reasons         []string                         `json:"reasons"`
	LatestCursor    string                           `json:"latestCursor,omitempty"`
}

type EventsQuery struct {
	Owner      workloadtypes.ActivityOwner
	SessionID  string
	From       time.Time
	To         time.Time
	Cursor     string
	Limit      int
	Kinds      []string
	Operations []string
	Executions []string
	Risks      []string
	Path       string
	Domain     string
	IP         string
}

type EventsPage struct {
	Records        []workloadtypes.ActivityRecord   `json:"records"`
	NextCursor     string                           `json:"nextCursor,omitempty"`
	Coverage       []workloadtypes.CoverageInterval `json:"coverage"`
	QueryTruncated bool                             `json:"queryTruncated"`
}

type ExecutionsQuery struct {
	Owner     workloadtypes.ActivityOwner
	SessionID string
	ID        string
	RootsOnly bool
}

type ExecutionNode struct {
	Execution         workloadtypes.Execution `json:"execution"`
	ActivityCounts    map[string]uint64       `json:"activityCounts"`
	ParentUnavailable bool                    `json:"parentUnavailable,omitempty"`
	Children          []ExecutionNode         `json:"children"`
}

type ExecutionsResult struct {
	Roots    []ExecutionNode                  `json:"roots"`
	Coverage []workloadtypes.CoverageInterval `json:"coverage"`
}

type CoverageQuery struct {
	Owner      workloadtypes.ActivityOwner
	SessionID  string
	From       time.Time
	To         time.Time
	Subsystems []string
}

type CoverageResult struct {
	Intervals []workloadtypes.CoverageInterval `json:"intervals"`
	Current   []workloadtypes.CoverageInterval `json:"current"`
}

type RisksQuery struct {
	Owner      workloadtypes.ActivityOwner
	SessionID  string
	From       time.Time
	To         time.Time
	Severities []string
	Rules      []string
	Executions []string
}

type RisksResult struct {
	Findings []risk.Finding `json:"findings"`
}
