package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	tuirender "github.com/vibe-agi/hideout/internal/tui/render"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	queryThresholdP95MS  = 250.0
	renderThresholdP95MS = 100.0
	fixtureRecordCount   = 4000
)

type metric struct {
	Unit            string    `json:"unit"`
	Samples         []float64 `json:"samples"`
	P50             float64   `json:"p50"`
	P95             float64   `json:"p95"`
	Maximum         float64   `json:"maximum"`
	ThresholdP95    float64   `json:"thresholdP95"`
	ThresholdPassed bool      `json:"thresholdPassed"`
}

type evidence struct {
	Schema      string `json:"schema"`
	GeneratedAt string `json:"generatedAt"`
	Result      string `json:"result"`
	Runtime     struct {
		GoVersion  string `json:"goVersion"`
		GOOS       string `json:"goos"`
		GOARCH     string `json:"goarch"`
		GOMAXPROCS int    `json:"gomaxprocs"`
	} `json:"runtime"`
	Methodology struct {
		Clock        string `json:"clock"`
		Samples      int    `json:"samples"`
		Warmups      int    `json:"warmups"`
		Percentile   string `json:"percentile"`
		RecordCount  int    `json:"recordCount"`
		RenderedRows int    `json:"renderedRows"`
	} `json:"methodology"`
	Query  metric `json:"query"`
	Render metric `json:"render"`
}

type queryFixture struct {
	service *workloadquery.Service
	owner   workloadtypes.ActivityOwner
	session string
	at      time.Time
}

var (
	querySink  uint64
	renderSink string
)

func main() {
	var (
		out     string
		samples int
		warmups int
	)
	flag.StringVar(&out, "out", "", "private JSON evidence output")
	flag.IntVar(&samples, "samples", 30, "recorded sample count")
	flag.IntVar(&warmups, "warmups", 5, "unrecorded warmup count")
	flag.Parse()

	if err := run(out, samples, warmups); err != nil {
		fmt.Fprintf(os.Stderr, "performance-local: %v\n", err)
		os.Exit(1)
	}
}

func run(out string, samples, warmups int) error {
	if out == "" || !filepath.IsAbs(out) {
		return errors.New("--out must be an absolute path")
	}
	if samples < 5 || samples > 1000 || warmups < 0 || warmups > 100 {
		return errors.New("sample bounds are invalid")
	}
	fixture, err := newQueryFixture()
	if err != nil {
		return err
	}

	var renderData tuirender.ActivityData
	querySamples, err := measure(samples, warmups, func() error {
		summary, err := fixture.service.Summary(
			context.Background(),
			workloadquery.SummaryQuery{
				Owner: fixture.owner, SessionID: fixture.session,
			},
		)
		if err != nil {
			return err
		}
		events, err := fixture.service.Events(
			context.Background(),
			workloadquery.EventsQuery{
				Owner: fixture.owner, SessionID: fixture.session,
				Limit: workloadquery.MaximumLimit,
				Kinds: []string{workloadtypes.ActivityFile},
				Path:  "/workspace/project",
			},
		)
		if err != nil {
			return err
		}
		executions, err := fixture.service.Executions(
			context.Background(),
			workloadquery.ExecutionsQuery{
				Owner: fixture.owner, SessionID: fixture.session,
			},
		)
		if err != nil {
			return err
		}
		coverage, err := fixture.service.Coverage(
			context.Background(),
			workloadquery.CoverageQuery{
				Owner: fixture.owner, SessionID: fixture.session,
			},
		)
		if err != nil {
			return err
		}
		risks, err := fixture.service.Risks(
			context.Background(),
			workloadquery.RisksQuery{
				Owner: fixture.owner, SessionID: fixture.session,
			},
		)
		if err != nil {
			return err
		}
		if len(events.Records) != workloadquery.MaximumLimit ||
			len(executions.Roots) != 1 ||
			len(coverage.Current) != 1 ||
			len(risks.Findings) != 1 {
			return errors.New("query fixture returned an incomplete production view")
		}
		querySink = summary.Counts[workloadtypes.ActivityFile] +
			uint64(len(events.Records)) +
			uint64(len(executions.Roots)) +
			uint64(len(coverage.Intervals)) +
			uint64(len(risks.Findings))
		renderData = tuirender.ActivityData{
			Owner: fixture.owner, Summary: summary, Events: events,
			Executions: executions, Coverage: coverage, Risks: risks,
		}
		return nil
	})
	if err != nil {
		return err
	}

	state := liveconsole.State{
		StreamHealth: liveconsole.StreamHealth{State: liveconsole.HealthLive},
	}
	renderSamples, err := measure(samples, warmups, func() error {
		rendered := tuirender.Activity(tuirender.ActivityInput{
			State: state, SessionID: fixture.session,
			Tab: tuirender.ActivityTabAll, Data: renderData,
			Loaded: true, Now: fixture.at.Add(time.Hour),
		}, tuirender.Options{
			Width: 150, Height: 40, Unicode: true, NoColor: true,
		})
		if len(rendered) < 100 || !strings.HasPrefix(rendered, "Hideout ") {
			return errors.New("activity render did not produce the expected HUD")
		}
		renderSink = rendered
		return nil
	})
	if err != nil {
		return err
	}

	result := evidence{
		Schema:      "hideout.release-performance-local/v1",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Result:      "passed",
		Query:       summarize(querySamples, queryThresholdP95MS),
		Render:      summarize(renderSamples, renderThresholdP95MS),
	}
	result.Runtime.GoVersion = runtime.Version()
	result.Runtime.GOOS = runtime.GOOS
	result.Runtime.GOARCH = runtime.GOARCH
	result.Runtime.GOMAXPROCS = runtime.GOMAXPROCS(0)
	result.Methodology.Clock = "go-monotonic-time.Since"
	result.Methodology.Samples = samples
	result.Methodology.Warmups = warmups
	result.Methodology.Percentile = "nearest-rank-ceiling"
	result.Methodology.RecordCount = fixtureRecordCount
	result.Methodology.RenderedRows = workloadquery.MaximumLimit
	if !result.Query.ThresholdPassed || !result.Render.ThresholdPassed {
		result.Result = "failed"
	}
	if result.Result != "passed" {
		return fmt.Errorf(
			"threshold failed: query p95 %.3fms render p95 %.3fms",
			result.Query.P95,
			result.Render.P95,
		)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(
		out,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(result)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf(
		"performance-local: passed query-p95=%.3fms render-p95=%.3fms evidence=%s\n",
		result.Query.P95,
		result.Render.P95,
		out,
	)
	return nil
}

func measure(
	samples,
	warmups int,
	work func() error,
) ([]float64, error) {
	result := make([]float64, 0, samples)
	for index := 0; index < warmups+samples; index++ {
		started := time.Now()
		if err := work(); err != nil {
			return nil, err
		}
		elapsed := float64(time.Since(started).Nanoseconds()) / 1e6
		if index >= warmups {
			result = append(result, elapsed)
		}
	}
	return result, nil
}

func summarize(samples []float64, threshold float64) metric {
	values := append([]float64(nil), samples...)
	sort.Float64s(values)
	p50 := nearestRank(values, 50)
	p95 := nearestRank(values, 95)
	return metric{
		Unit: "milliseconds", Samples: samples,
		P50: p50, P95: p95, Maximum: values[len(values)-1],
		ThresholdP95: threshold, ThresholdPassed: p95 <= threshold,
	}
}

func nearestRank(sorted []float64, percentile int) float64 {
	index := int(math.Ceil(float64(len(sorted))*float64(percentile)/100.0)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func newQueryFixture() (queryFixture, error) {
	owner, err := workloadtypes.NewReusableOwner(
		"env_performance_reference",
		"lima",
		"incarnation-performance-reference",
	)
	if err != nil {
		return queryFixture{}, err
	}
	session := "ses_performance_reference_001"
	at := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	executionID, err := workloadtypes.NewExecutionID(
		workloadtypes.ExecutionIdentityInput{
			Owner: owner, SessionID: session,
			GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
			ObserverGeneration: 1, PID: 4242, ExecSequence: 1,
			StartedAtMonoNS: 1_000_000,
		},
	)
	if err != nil {
		return queryFixture{}, err
	}
	execution := workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: executionID,
		Owner: owner, SessionID: session,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: 4242, TID: 4242,
		ExecSequence: 1, StartedAtMonoNS: 1_000_000, StartedAt: at,
		Executable: "/usr/local/bin/claude",
		Argv:       []string{"claude", "--safe-mode"},
		Cwd:        "/workspace",
		Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "developer", Group: "developer",
		},
	}
	coverage := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema,
		ID:     "cov_performance_file_0001",
		Owner:  owner, SessionID: session,
		Subsystem: workloadtypes.SubsystemFile,
		State:     workloadtypes.CoverageAvailable, Reason: "collector-ready",
		CollectorGeneration: 1, StartSequence: 1, StartedAt: at,
	}
	if err := coverage.Validate(); err != nil {
		return queryFixture{}, err
	}
	records := make([]workloadtypes.ActivityRecord, 0, fixtureRecordCount)
	for index := 0; index < fixtureRecordCount; index++ {
		path := fmt.Sprintf("/workspace/project/file-%04d.txt", index%1000)
		if index == 0 {
			path = "/etc/hideout-performance.conf"
		}
		pathClass := "workspace"
		if index == 0 {
			pathClass = "external"
		}
		record := workloadtypes.ActivityRecord{
			Schema: workloadtypes.ActivityRecordSchema,
			ID:     fmt.Sprintf("act_performance%08d", index),
			Owner:  owner, SessionID: session,
			Actor: &workloadtypes.Actor{
				ExecutionID: executionID, PID: 4242,
				UID: 1000, GID: 1000, User: "developer", Group: "developer",
			},
			Kind: workloadtypes.ActivityFile, Operation: "write",
			Subject: workloadtypes.FileSubject{
				Kind: workloadtypes.ActivityFile, Path: path,
				PathState: "resolved", PathClass: pathClass,
				FileType: "regular", Device: 8, Inode: uint64(index + 1),
			},
			Outcome: workloadtypes.Outcome{
				Status: workloadtypes.OutcomeSucceeded,
			},
			Count: 1, Bytes: 128,
			FirstAt:       at.Add(time.Duration(index) * time.Millisecond),
			LastAt:        at.Add(time.Duration(index) * time.Millisecond),
			FirstSequence: uint64(index + 1),
			LastSequence:  uint64(index + 1),
			Attribution:   workloadtypes.AttributionExact,
			CoverageID:    coverage.ID, RedactionStatus: workloadtypes.RedactionPassed,
		}
		if err := record.Validate(); err != nil {
			return queryFixture{}, fmt.Errorf("record %d: %w", index, err)
		}
		records = append(records, record)
	}
	engine, err := workloadrisk.NewEngine(workloadrisk.DefaultRuleSet())
	if err != nil {
		return queryFixture{}, err
	}
	findings, err := engine.Evaluate([]workloadrisk.Evidence{{
		Activity: records[0], PolicyStatus: workloadrisk.PolicyNotEvaluated,
	}})
	if err != nil {
		return queryFixture{}, err
	}
	if len(findings) != 1 {
		return queryFixture{}, errors.New("risk fixture did not produce one finding")
	}

	source := workloadquery.NewMemorySource()
	if err := source.Replace(workloadquery.Snapshot{
		Revision: "rev_performance_reference_0001",
		Owner:    owner, Records: records,
		Executions: []workloadtypes.Execution{execution},
		Coverage:   []workloadtypes.CoverageInterval{coverage},
		Risks:      findings,
		Retention: workloadquery.RetentionState{
			EarliestAt: at,
			LatestAt:   at.Add(fixtureRecordCount * time.Millisecond),
			UsedBytes:  4 << 20, LimitBytes: 64 << 20,
			Reasons: []string{},
		},
	}); err != nil {
		return queryFixture{}, err
	}
	service, err := workloadquery.NewService(workloadquery.Options{
		Source: source,
		CursorKey: []byte(
			"0123456789abcdef0123456789abcdef",
		),
	})
	if err != nil {
		return queryFixture{}, err
	}
	return queryFixture{
		service: service, owner: owner, session: session, at: at,
	}, nil
}
