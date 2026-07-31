package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	"github.com/vibe-agi/hideout/internal/workloadobs/coverage"
	workloadredact "github.com/vibe-agi/hideout/internal/workloadobs/redact"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityServiceRegistersBeforeIngestAndAccountsEveryLossSource(t *testing.T) {
	base := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	ticks := 0
	service := newActivityService(func() time.Time {
		ticks++
		return base.Add(time.Duration(ticks) * time.Millisecond)
	})
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	if expectation.ObserverStreamToken.Validate() != nil {
		t.Fatal("daemon did not issue observer stream authority")
	}
	ready := activityReadyFixture(preparation, 3141, allAvailableCoverage())
	if err := service.RegisterBoundary(preparation.SessionID, ready); err != nil {
		t.Fatal(err)
	}

	heartbeat := activityEnvelope(preparation, 3141, 2, 1, "collector.heartbeat", `{
		"latestSequence":1,
		"kernelDropped":0,
		"ringDropped":0
	}`)
	if err := service.Ingest(preparation.SessionID, heartbeat); err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(preparation.SessionID, heartbeat); err != nil {
		t.Fatal(err)
	}
	gapped := activityEnvelope(
		preparation,
		3141,
		2,
		3,
		"collector.heartbeat",
		`{"latestSequence":3,"kernelDropped":0,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, gapped); err != nil {
		t.Fatal(err)
	}
	loss := activityEnvelope(
		preparation,
		3141,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.loss",
		`{"dropped":4,"droppedBytes":512,"reason":"observer-send-queue-overflow","scope":"guest-observer-transport"}`,
	)
	if err := service.Ingest(preparation.SessionID, loss); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("registered activity session disappeared")
	}
	if snapshot.Accepted != 3 || snapshot.Duplicates != 1 ||
		snapshot.Missing != 1 || snapshot.ReportedDropped != 4 ||
		snapshot.DroppedBytes != 512 {
		t.Fatalf("loss accounting=%+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoveragePartial ||
			summary.CurrentReason != coverage.ReasonTransportDrop ||
			summary.DroppedEventCount != 5 {
			t.Fatalf("%s coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceBindsOwnerRetentionBeforeRegistration(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	now := time.Date(2026, 7, 29, 13, 30, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root: root, ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes: 8 << 20, GlobalBytes: 32 << 20,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	service := newActivityService(func() time.Time { return now })
	service.setPersistentStore(persistent)
	preparation := activityPreparationFixture(t)
	preparation.Retention = workloadtypes.ActivityRetentionPolicy{
		MaxBytes: 4 << 20, MaxAgeSeconds: 2 * 60 * 60,
	}
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(expectation.ObserverStreamToken.Destroy)

	stats, err := persistent.OwnerStats(preparation.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LimitBytes != uint64(preparation.Retention.MaxBytes) ||
		stats.MaxAgeSeconds != preparation.Retention.MaxAgeSeconds {
		t.Fatalf("bound owner retention=%+v", stats)
	}

	conflict := preparation
	conflict.SessionID = "ses_activity_conflict"
	conflict.Retention.MaxBytes += 1024
	conflictExpectation, err := service.Prepare(conflict)
	if err != nil {
		t.Fatalf("existing owner attach with new desired policy: %v", err)
	}
	t.Cleanup(conflictExpectation.ObserverStreamToken.Destroy)
	if _, exists := service.Snapshot(conflict.SessionID); !exists {
		t.Fatal("existing owner did not retain its bound policy for a new session")
	}
	stats, err = persistent.OwnerStats(preparation.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LimitBytes != uint64(preparation.Retention.MaxBytes) {
		t.Fatalf("new desired policy rewrote existing owner: %+v", stats)
	}
}

func TestActivityServiceRejectsForeignAndMalformedLossWithoutAttribution(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 99, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	foreign := activityEnvelope(preparation, 100, 0, 1, "process.exec", `{"pid":42}`)
	if err := service.Ingest(preparation.SessionID, foreign); !errors.Is(err, sessionwire.ErrObserverIdentity) {
		t.Fatalf("foreign boundary error=%v", err)
	}
	malformed := activityEnvelope(
		preparation,
		99,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.loss",
		`{"dropped":2,"droppedBytes":10,"reason":"observer-send-queue-overflow","scope":"guest-observer-transport","secret":"must-not-pass"}`,
	)
	if err := service.Ingest(preparation.SessionID, malformed); !errors.Is(err, errActivityObservationInvalid) {
		t.Fatalf("malformed loss error=%v", err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity snapshot missing")
	}
	if snapshot.Accepted != 0 || snapshot.Invalid != 2 || snapshot.LastKind != "" {
		t.Fatalf("invalid observations were attributed: %+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoveragePartial ||
			summary.CurrentReason != coverage.ReasonInvalidFrame ||
			summary.DroppedEventCount != 2 {
			t.Fatalf("%s invalid-frame coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceRejectsMalformedControlBeforeConsumingSequence(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 101, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	malformed := activityEnvelope(
		preparation,
		101,
		3,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0,"unexpected":true}`,
	)
	if err := service.Ingest(preparation.SessionID, malformed); !errors.Is(err, errActivityObservationInvalid) {
		t.Fatalf("malformed heartbeat error=%v", err)
	}
	valid := activityEnvelope(
		preparation,
		101,
		3,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, valid); err != nil {
		t.Fatalf("valid retry after rejected frame: %v", err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity snapshot missing")
	}
	if snapshot.Accepted != 1 || snapshot.Duplicates != 0 ||
		snapshot.Missing != 0 || snapshot.Invalid != 1 {
		t.Fatalf("rejected frame consumed observer sequence: %+v", snapshot)
	}
}

func TestActivityServiceAcceptsOnlyBoundTransportDrainMarker(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 101, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	finalHeartbeat := activityEnvelope(
		preparation,
		101,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0,"final":true}`,
	)
	goodbye := activityEnvelope(
		preparation,
		101,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.goodbye",
		`{"reason":"relay-drained"}`,
	)
	if err := service.IngestBatch(
		preparation.SessionID,
		[]sessionwire.ObservationEnvelope{finalHeartbeat, goodbye},
	); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || snapshot.Accepted != 2 || snapshot.LastKind != "collector.goodbye" ||
		snapshot.Invalid != 0 {
		t.Fatalf("transport drain snapshot=%+v present=%v", snapshot, ok)
	}

	wrongCPU := goodbye
	wrongCPU.CPU = 7
	wrongCPU.Sequence = 1
	if err := service.Ingest(preparation.SessionID, wrongCPU); !errors.Is(err, errActivityObservationInvalid) {
		t.Fatalf("wrong-CPU drain marker error=%v", err)
	}
}

func TestActivityServiceRejectsDrainWithoutExactFinalCollectorReceipt(t *testing.T) {
	tests := []struct {
		name      string
		heartbeat string
	}{
		{name: "missing heartbeat"},
		{
			name:      "non-final heartbeat",
			heartbeat: `{"latestSequence":1,"kernelDropped":0,"ringDropped":0}`,
		},
		{
			name: "inexact final file counters",
			heartbeat: `{
				"latestSequence":1,
				"kernelDropped":0,
				"ringDropped":0,
				"file":{"matchedEvents":2,"reservedEvents":1},
				"final":true
			}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newActivityService(nil)
			preparation := activityPreparationFixture(t)
			if _, err := service.Prepare(preparation); err != nil {
				t.Fatal(err)
			}
			if err := service.RegisterBoundary(
				preparation.SessionID,
				activityReadyFixture(preparation, 101, allAvailableCoverage()),
			); err != nil {
				t.Fatal(err)
			}
			if test.heartbeat != "" {
				heartbeat := activityEnvelope(
					preparation,
					101,
					sessionwire.ObserverControlCPU,
					1,
					"collector.heartbeat",
					test.heartbeat,
				)
				if err := service.Ingest(preparation.SessionID, heartbeat); err != nil {
					t.Fatalf("heartbeat: %v", err)
				}
			}
			goodbye := activityEnvelope(
				preparation,
				101,
				sessionwire.ObserverTransportCPU,
				1,
				"collector.goodbye",
				`{"reason":"relay-drained"}`,
			)
			if err := service.Ingest(preparation.SessionID, goodbye); !errors.Is(
				err,
				errActivityObservationInvalid,
			) {
				t.Fatalf("drain marker error=%v", err)
			}
		})
	}
}

func TestActivityServiceInvalidatesFinalReceiptAfterLaterObserverFrame(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 101, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	finalHeartbeat := activityEnvelope(
		preparation,
		101,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0,"final":true}`,
	)
	if err := service.Ingest(preparation.SessionID, finalHeartbeat); err != nil {
		t.Fatal(err)
	}
	laterHeartbeat := activityEnvelope(
		preparation,
		101,
		sessionwire.ObserverControlCPU,
		2,
		"collector.heartbeat",
		`{"latestSequence":2,"kernelDropped":0,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, laterHeartbeat); !errors.Is(
		err,
		errActivityObservationInvalid,
	) {
		t.Fatalf("post-final observer frame error=%v", err)
	}
	goodbye := activityEnvelope(
		preparation,
		101,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.goodbye",
		`{"reason":"relay-drained"}`,
	)
	if err := service.Ingest(preparation.SessionID, goodbye); !errors.Is(
		err,
		errActivityObservationInvalid,
	) {
		t.Fatalf("goodbye after invalidated final receipt error=%v", err)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || snapshot.Accepted != 1 || snapshot.Invalid != 2 ||
		snapshot.LastKind != "collector.heartbeat" {
		t.Fatalf("invalidated final receipt snapshot=%+v present=%v", snapshot, ok)
	}
}

func TestActivityServiceBindsGracefulCompletionToGoodbyeAndTransportEOF(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	ready := activityReadyFixture(preparation, 111, allAvailableCoverage())
	if err := service.RegisterBoundary(preparation.SessionID, ready); err != nil {
		t.Fatal(err)
	}
	finalHeartbeat := activityEnvelope(
		preparation,
		111,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0,"final":true}`,
	)
	goodbye := activityEnvelope(
		preparation,
		111,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.goodbye",
		`{"reason":"relay-drained"}`,
	)
	if err := service.IngestBatch(
		preparation.SessionID,
		[]sessionwire.ObservationEnvelope{finalHeartbeat, goodbye},
	); err != nil {
		t.Fatal(err)
	}
	beforeEOF, ok := service.Snapshot(preparation.SessionID)
	if !ok || !beforeEOF.GoodbyeAccepted || beforeEOF.TransportClosed {
		t.Fatalf("pre-EOF receipt=%+v present=%v", beforeEOF, ok)
	}
	if err := service.ObserverExited(preparation.SessionID, nil); err != nil {
		t.Fatal(err)
	}
	afterEOF, ok := service.Snapshot(preparation.SessionID)
	if !ok || !afterEOF.ObserverClosed || !afterEOF.TransportClosed {
		t.Fatalf("post-EOF receipt=%+v present=%v", afterEOF, ok)
	}
	for _, subsystem := range activitySubsystems {
		if summary := afterEOF.Coverage[subsystem]; summary.CurrentState != workloadtypes.CoverageUnavailable ||
			summary.CurrentReason != coverage.ReasonTargetExited {
			t.Fatalf("%s post-EOF coverage=%+v", subsystem, summary)
		}
	}
	completion := &sessionwire.SupervisorActivityCompletion{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		CgroupID: 111, ObserverGeneration: preparation.ObserverGeneration,
		BoundaryState: workloadtypes.BoundaryRemoved,
		Coverage:      allAvailableCoverage(),
		CleanupProved: true,
	}
	if err := service.SessionExited(preparation.SessionID, completion, nil); err != nil {
		t.Fatal(err)
	}
	closed, ok := service.Snapshot(preparation.SessionID)
	if !ok || !closed.SessionClosed || closed.Invalid != 0 {
		t.Fatalf("graceful activity completion=%+v present=%v", closed, ok)
	}
	for _, subsystem := range activitySubsystems {
		if summary := closed.Coverage[subsystem]; summary.CurrentState != workloadtypes.CoverageUnavailable ||
			summary.CurrentReason != coverage.ReasonTargetExited {
			t.Fatalf("%s graceful coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceRejectsCleanTransportCloseWithoutDrainReceipt(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 112, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserverExited(preparation.SessionID, nil); !errors.Is(
		err,
		errActivityObservationInvalid,
	) {
		t.Fatalf("receipt-free transport close error=%v", err)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || !snapshot.ObserverClosed || snapshot.TransportClosed ||
		snapshot.GoodbyeAccepted || snapshot.Invalid != 1 {
		t.Fatalf("receipt-free transport close=%+v present=%v", snapshot, ok)
	}
}

func TestActivityServiceRejectsCleanupProofBeforeTransportReceipt(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	ready := activityReadyFixture(preparation, 113, allAvailableCoverage())
	if err := service.RegisterBoundary(preparation.SessionID, ready); err != nil {
		t.Fatal(err)
	}
	finalHeartbeat := activityEnvelope(
		preparation,
		113,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0,"final":true}`,
	)
	goodbye := activityEnvelope(
		preparation,
		113,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.goodbye",
		`{"reason":"relay-drained"}`,
	)
	if err := service.IngestBatch(
		preparation.SessionID,
		[]sessionwire.ObservationEnvelope{finalHeartbeat, goodbye},
	); err != nil {
		t.Fatal(err)
	}
	completion := &sessionwire.SupervisorActivityCompletion{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		CgroupID: 113, ObserverGeneration: preparation.ObserverGeneration,
		BoundaryState: workloadtypes.BoundaryRemoved,
		Coverage:      allAvailableCoverage(),
		CleanupProved: true,
	}
	if err := service.SessionExited(
		preparation.SessionID,
		completion,
		nil,
	); !errors.Is(err, errActivityObservationInvalid) {
		t.Fatalf("pre-EOF cleanup proof error=%v", err)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || !snapshot.GoodbyeAccepted || snapshot.TransportClosed ||
		!snapshot.SessionClosed || snapshot.Invalid != 1 {
		t.Fatalf("pre-EOF cleanup proof=%+v present=%v", snapshot, ok)
	}
	for _, subsystem := range activitySubsystems {
		if summary := snapshot.Coverage[subsystem]; summary.CurrentReason != coverage.ReasonCleanupUnproved {
			t.Fatalf("%s pre-EOF cleanup coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceRegistersCoverageAtOneBoundaryInstant(t *testing.T) {
	base := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	ticks := 0
	service := newActivityService(func() time.Time {
		ticks++
		return base.Add(time.Duration(ticks) * time.Millisecond)
	})
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 102, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity snapshot missing")
	}
	requiredSubsystems := []string{
		workloadtypes.SubsystemProcess,
		workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork,
		workloadtypes.SubsystemDNS,
	}
	if len(snapshot.Intervals) != len(requiredSubsystems) {
		t.Fatalf(
			"coverage subsystem rows=%d want %d: %#v",
			len(snapshot.Intervals),
			len(requiredSubsystems),
			snapshot.Intervals,
		)
	}
	var startedAt time.Time
	for _, subsystem := range requiredSubsystems {
		intervals := snapshot.Intervals[subsystem]
		if len(intervals) != 1 {
			t.Fatalf("%s intervals=%d want 1", subsystem, len(intervals))
		}
		if startedAt.IsZero() {
			startedAt = intervals[0].StartedAt
			continue
		}
		if !intervals[0].StartedAt.Equal(startedAt) {
			t.Fatalf(
				"boundary coverage timestamps diverged: %s=%s want %s",
				subsystem,
				intervals[0].StartedAt,
				startedAt,
			)
		}
	}
}

func TestActivityServiceCompletionDoesNotDoubleCountHostObservedLoss(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	ready := activityReadyFixture(preparation, 103, allAvailableCoverage())
	if err := service.RegisterBoundary(preparation.SessionID, ready); err != nil {
		t.Fatal(err)
	}

	// Starting at sequence 3 proves that the first two observations were lost.
	gapped := activityEnvelope(
		preparation,
		103,
		7,
		3,
		"collector.heartbeat",
		`{"latestSequence":3,"kernelDropped":0,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, gapped); err != nil {
		t.Fatal(err)
	}
	terminalCoverage := allAvailableCoverage()
	for index := range terminalCoverage {
		terminalCoverage[index].State = workloadtypes.CoveragePartial
		terminalCoverage[index].Reason = "observer-sequence-gap"
		terminalCoverage[index].Evidence = []string{"observer-sequence-gap"}
		terminalCoverage[index].DroppedEventCount = 2
	}
	completion := &sessionwire.SupervisorActivityCompletion{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		CgroupID: 103, ObserverGeneration: preparation.ObserverGeneration,
		BoundaryState: workloadtypes.BoundaryRemoved,
		Coverage:      terminalCoverage,
		CleanupProved: true,
	}
	finalHeartbeat := activityEnvelope(
		preparation,
		103,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":0,"ringDropped":0,"final":true}`,
	)
	goodbye := activityEnvelope(
		preparation,
		103,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.goodbye",
		`{"reason":"relay-drained"}`,
	)
	if err := service.IngestBatch(
		preparation.SessionID,
		[]sessionwire.ObservationEnvelope{finalHeartbeat, goodbye},
	); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserverExited(preparation.SessionID, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.SessionExited(preparation.SessionID, completion, nil); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity snapshot missing")
	}
	if snapshot.Missing != 2 || snapshot.ReportedDropped != 0 {
		t.Fatalf("global loss accounting double-counted terminal summaries: %+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.DroppedEventCount != 2 {
			t.Fatalf("%s terminal loss=%d want 2", subsystem, summary.DroppedEventCount)
		}
	}
}

func TestActivityServiceHeartbeatsAccountOnlyCounterDeltas(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 104, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	first := activityEnvelope(
		preparation,
		104,
		4,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":2,"ringDropped":3}`,
	)
	second := activityEnvelope(
		preparation,
		104,
		4,
		2,
		"collector.heartbeat",
		`{"latestSequence":2,"kernelDropped":5,"ringDropped":4}`,
	)
	if err := service.Ingest(preparation.SessionID, first); err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(preparation.SessionID, second); err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(preparation.SessionID, second); err != nil {
		t.Fatalf("duplicate heartbeat: %v", err)
	}
	rollback := activityEnvelope(
		preparation,
		104,
		4,
		3,
		"collector.heartbeat",
		`{"latestSequence":3,"kernelDropped":4,"ringDropped":4}`,
	)
	if err := service.Ingest(preparation.SessionID, rollback); !errors.Is(err, errActivityObservationInvalid) {
		t.Fatalf("counter rollback error=%v", err)
	}
	retry := activityEnvelope(
		preparation,
		104,
		4,
		3,
		"collector.heartbeat",
		`{"latestSequence":3,"kernelDropped":5,"ringDropped":4}`,
	)
	if err := service.Ingest(preparation.SessionID, retry); err != nil {
		t.Fatalf("valid retry after rollback: %v", err)
	}

	snapshot, _ := service.Snapshot(preparation.SessionID)
	if snapshot.Accepted != 3 || snapshot.Duplicates != 1 ||
		snapshot.Invalid != 1 || snapshot.KernelDropped != 5 ||
		snapshot.RingDropped != 4 {
		t.Fatalf("heartbeat accounting=%+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		if dropped := snapshot.Coverage[subsystem].DroppedEventCount; dropped != 10 {
			t.Fatalf("%s heartbeat+invalid loss=%d want 10", subsystem, dropped)
		}
	}
}

func TestActivityServicePublishesDetailedFinalCollectorCounters(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 106, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	heartbeat := activityEnvelope(
		preparation,
		106,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{
			"latestSequence":1,
			"kernelDropped":0,
			"ringDropped":0,
			"local":{"process":0,"file":0,"network":0,"dns":0},
			"file":{
				"matchedEvents":57,
				"reservedEvents":57,
				"ringbufDrops":0,
				"stateDrops":0,
				"stateDegradations":6,
				"pathFailures":0,
				"identityFailures":0
			},
			"final":true
		}`,
	)
	if err := service.Ingest(preparation.SessionID, heartbeat); err != nil {
		t.Fatal(err)
	}
	goodbye := activityEnvelope(
		preparation,
		106,
		sessionwire.ObserverTransportCPU,
		1,
		"collector.goodbye",
		`{"reason":"relay-drained"}`,
	)
	if err := service.Ingest(preparation.SessionID, goodbye); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserverExited(preparation.SessionID, nil); err != nil {
		t.Fatal(err)
	}
	completion := &sessionwire.SupervisorActivityCompletion{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		CgroupID: 106, ObserverGeneration: preparation.ObserverGeneration,
		BoundaryState: workloadtypes.BoundaryRemoved,
		Coverage:      allAvailableCoverage(),
		CleanupProved: true,
	}
	if err := service.SessionExited(preparation.SessionID, completion, nil); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity snapshot missing")
	}
	want := map[string]string{
		"kernel-dropped":          "0",
		"ring-dropped":            "0",
		"local-process-dropped":   "0",
		"local-file-dropped":      "0",
		"local-network-dropped":   "0",
		"local-dns-dropped":       "0",
		"file-matched-events":     "57",
		"file-reserved-events":    "57",
		"file-ringbuf-drops":      "0",
		"file-state-drops":        "0",
		"file-state-degradations": "6",
		"file-path-failures":      "0",
		"file-identity-failures":  "0",
	}
	for _, subsystem := range activitySubsystems {
		intervals := snapshot.Intervals[subsystem]
		if len(intervals) == 0 {
			t.Fatalf("%s terminal coverage interval missing", subsystem)
		}
		terminal := intervals[len(intervals)-1]
		if terminal.Reason != coverage.ReasonTargetExited {
			t.Fatalf("%s terminal reason=%q", subsystem, terminal.Reason)
		}
		got := make(map[string]string, len(terminal.Evidence))
		for _, current := range terminal.Evidence {
			if _, duplicate := got[current.Code]; duplicate {
				t.Fatalf("%s duplicate terminal evidence code %q", subsystem, current.Code)
			}
			got[current.Code] = current.Value
		}
		for code, value := range want {
			if got[code] != value {
				t.Fatalf("%s evidence %s=%q want %q", subsystem, code, got[code], value)
			}
		}
	}
}

func TestActivityServiceFileDegradationMakesOnlyFileCoveragePartial(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 108, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	heartbeat := activityEnvelope(
		preparation,
		108,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{
			"latestSequence":1,
			"kernelDropped":0,
			"ringDropped":0,
			"local":{"process":0,"file":0,"network":0,"dns":0},
			"file":{
				"matchedEvents":3,
				"reservedEvents":3,
				"ringbufDrops":0,
				"stateDrops":0,
				"stateDegradations":1,
				"pathFailures":1,
				"identityFailures":0
			}
		}`,
	)
	if err := service.Ingest(preparation.SessionID, heartbeat); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity snapshot missing")
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if subsystem == workloadtypes.SubsystemFile {
			if summary.CurrentState != workloadtypes.CoveragePartial ||
				summary.CurrentReason != coverage.ReasonCollectorPartial ||
				summary.DroppedEventCount != 0 {
				t.Fatalf("file degradation coverage=%+v", summary)
			}
			intervals := snapshot.Intervals[subsystem]
			current := intervals[len(intervals)-1]
			wantEvidence := map[string]string{
				"file-state-degradation": "1",
				"file-path-failure":      "1",
			}
			for _, evidence := range current.Evidence {
				delete(wantEvidence, evidence.Code)
			}
			if len(wantEvidence) != 0 {
				t.Fatalf("file degradation evidence missing: %v in %+v", wantEvidence, current)
			}
			continue
		}
		if summary.CurrentState != workloadtypes.CoverageAvailable ||
			summary.DroppedEventCount != 0 {
			t.Fatalf("%s coverage changed by file-only degradation: %+v", subsystem, summary)
		}
	}
}

func TestActivityServiceDetailedFileLossDoesNotDegradeOtherDomains(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 109, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	heartbeat := activityEnvelope(
		preparation,
		109,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{
			"latestSequence":1,
			"kernelDropped":2,
			"ringDropped":1,
			"local":{"process":0,"file":1,"network":0,"dns":0},
			"file":{
				"matchedEvents":3,
				"reservedEvents":2,
				"ringbufDrops":1,
				"stateDrops":1,
				"stateDegradations":0,
				"pathFailures":0,
				"identityFailures":0
			}
		}`,
	)
	if err := service.Ingest(preparation.SessionID, heartbeat); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity snapshot missing")
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if subsystem == workloadtypes.SubsystemFile {
			if summary.CurrentState != workloadtypes.CoveragePartial ||
				summary.CurrentReason != coverage.ReasonRingOverflow ||
				summary.DroppedEventCount != 3 {
				t.Fatalf("file loss coverage=%+v", summary)
			}
			continue
		}
		if summary.CurrentState != workloadtypes.CoverageAvailable ||
			summary.DroppedEventCount != 0 {
			t.Fatalf("%s coverage changed by file-only loss: %+v", subsystem, summary)
		}
	}
}

func TestActivityServiceRejectsDetailedCountersBeyondAggregate(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 107, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	heartbeat := activityEnvelope(
		preparation,
		107,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{
			"latestSequence":1,
			"kernelDropped":1,
			"ringDropped":0,
			"local":{"process":1,"file":0,"network":0,"dns":0},
			"file":{"stateDrops":1}
		}`,
	)
	if err := service.Ingest(preparation.SessionID, heartbeat); !errors.Is(
		err,
		errActivityObservationInvalid,
	) {
		t.Fatalf("detailed aggregate error=%v", err)
	}
}

func TestActivityServiceRejectsDetailedLossCatchUpWithoutAggregateDelta(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 110, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	first := activityEnvelope(
		preparation,
		110,
		sessionwire.ObserverControlCPU,
		1,
		"collector.heartbeat",
		`{"latestSequence":1,"kernelDropped":2,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, first); err != nil {
		t.Fatal(err)
	}
	catchUp := activityEnvelope(
		preparation,
		110,
		sessionwire.ObserverControlCPU,
		2,
		"collector.heartbeat",
		`{
			"latestSequence":2,
			"kernelDropped":2,
			"ringDropped":0,
			"local":{"process":0,"file":1,"network":0,"dns":0}
		}`,
	)
	if err := service.Ingest(preparation.SessionID, catchUp); !errors.Is(
		err,
		errActivityObservationInvalid,
	) {
		t.Fatalf("detailed counter catch-up error=%v", err)
	}
	retry := activityEnvelope(
		preparation,
		110,
		sessionwire.ObserverControlCPU,
		2,
		"collector.heartbeat",
		`{"latestSequence":2,"kernelDropped":2,"ringDropped":0}`,
	)
	if err := service.Ingest(preparation.SessionID, retry); err != nil {
		t.Fatalf("valid retry after detailed counter rejection: %v", err)
	}
}

func TestActivityServiceConcurrentObservationAndExitClosesExactlyOnce(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	ready := activityReadyFixture(preparation, 105, allAvailableCoverage())
	if err := service.RegisterBoundary(preparation.SessionID, ready); err != nil {
		t.Fatal(err)
	}
	completion := &sessionwire.SupervisorActivityCompletion{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		CgroupID: 105, ObserverGeneration: preparation.ObserverGeneration,
		BoundaryState: workloadtypes.BoundaryUnproved,
		Coverage:      allAvailableCoverage(),
		CleanupProved: false,
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	for cpu := uint64(0); cpu < 32; cpu++ {
		wait.Add(1)
		go func(cpu uint64) {
			defer wait.Done()
			<-start
			envelope := activityEnvelope(
				preparation,
				105,
				cpu,
				1,
				"collector.heartbeat",
				`{"latestSequence":1,"kernelDropped":0,"ringDropped":0}`,
			)
			err := service.Ingest(preparation.SessionID, envelope)
			if err != nil && !errors.Is(err, errActivitySessionClosed) {
				t.Errorf("concurrent ingest error=%v", err)
			}
		}(cpu)
	}
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		if err := service.ObserverExited(preparation.SessionID, io.EOF); err != nil {
			t.Errorf("observer exit error=%v", err)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		if err := service.SessionExited(preparation.SessionID, completion, nil); err != nil {
			t.Errorf("session exit error=%v", err)
		}
	}()
	close(start)
	wait.Wait()
	if err := service.SessionExited(preparation.SessionID, completion, nil); err != nil {
		t.Fatalf("repeated session exit error=%v", err)
	}

	snapshot, _ := service.Snapshot(preparation.SessionID)
	if !snapshot.ObserverClosed || !snapshot.SessionClosed {
		t.Fatalf("terminal activity state=%+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoverageUnavailable ||
			summary.CurrentReason != coverage.ReasonCleanupUnproved {
			t.Fatalf("%s terminal coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceClosesCoverageOnObserverAndSessionExit(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	ready := activityReadyFixture(preparation, 777, allAvailableCoverage())
	if err := service.RegisterBoundary(preparation.SessionID, ready); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserverExited(preparation.SessionID, errors.New("ssh channel ended")); err != nil {
		t.Fatal(err)
	}
	late := activityEnvelope(preparation, 777, 1, 1, "process.exec", `{"pid":42}`)
	if err := service.Ingest(preparation.SessionID, late); !errors.Is(err, errActivitySessionClosed) {
		t.Fatalf("post-observer-exit ingest error=%v", err)
	}
	afterObserver, _ := service.Snapshot(preparation.SessionID)
	if afterObserver.Accepted != 0 {
		t.Fatalf("post-observer-exit event was accepted: %+v", afterObserver)
	}
	for _, subsystem := range activitySubsystems {
		summary := afterObserver.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoverageUnavailable ||
			summary.CurrentReason != coverage.ReasonDaemonDisconnected ||
			summary.DroppedEventCount != 1 {
			t.Fatalf("%s observer-exit coverage=%+v", subsystem, summary)
		}
		intervals := afterObserver.Intervals[subsystem]
		if len(intervals) != 3 ||
			intervals[1].Reason != coverage.ReasonTransportDrop ||
			len(intervals[1].Evidence) != 1 ||
			intervals[1].Evidence[0].Code != "observer-stream-ended-unexpectedly" {
			t.Fatalf("%s observer-exit intervals=%+v", subsystem, intervals)
		}
	}

	completion := &sessionwire.SupervisorActivityCompletion{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		CgroupID: 777, ObserverGeneration: preparation.ObserverGeneration,
		BoundaryState: workloadtypes.BoundaryUnproved,
		Coverage:      allAvailableCoverage(),
		CleanupProved: false,
	}
	if err := service.SessionExited(preparation.SessionID, completion, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.SessionExited(preparation.SessionID, completion, nil); err != nil {
		t.Fatalf("idempotent session close failed: %v", err)
	}
	closed, _ := service.Snapshot(preparation.SessionID)
	if !closed.ObserverClosed || !closed.SessionClosed {
		t.Fatalf("terminal lifecycle=%+v", closed)
	}
	for _, subsystem := range activitySubsystems {
		summary := closed.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoverageUnavailable ||
			summary.CurrentReason != coverage.ReasonCleanupUnproved {
			t.Fatalf("%s terminal coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceAccountsUnexpectedSupervisorExitBeforeCleanup(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 780, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.SessionExited(
		preparation.SessionID,
		nil,
		errors.New("observer helper exited unsuccessfully"),
	); err != nil {
		t.Fatal(err)
	}

	snapshot, _ := service.Snapshot(preparation.SessionID)
	if snapshot.Invalid != 0 || !snapshot.SessionClosed || !snapshot.ObserverClosed {
		t.Fatalf("unexpected supervisor exit accounting=%+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoverageUnavailable ||
			summary.CurrentReason != coverage.ReasonCleanupUnproved ||
			summary.DroppedEventCount != 1 {
			t.Fatalf("%s unexpected supervisor-exit coverage=%+v", subsystem, summary)
		}
		intervals := snapshot.Intervals[subsystem]
		if len(intervals) != 3 ||
			intervals[1].Reason != coverage.ReasonTransportDrop ||
			intervals[2].Reason != coverage.ReasonCleanupUnproved ||
			len(intervals[1].Evidence) != 1 ||
			intervals[1].Evidence[0].Code != "observer-stream-ended-unexpectedly" {
			t.Fatalf("%s unexpected supervisor-exit intervals=%+v", subsystem, intervals)
		}
	}
}

func TestActivityServiceAccountsProtocolFailureBeforeClosingObserverCoverage(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 778, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserverExited(preparation.SessionID, sessionwire.ErrObserverCRC); err != nil {
		t.Fatal(err)
	}
	if err := service.SessionExited(
		preparation.SessionID,
		nil,
		sessionwire.ErrObserverCRC,
	); err != nil {
		t.Fatal(err)
	}

	snapshot, _ := service.Snapshot(preparation.SessionID)
	if snapshot.Invalid != 1 {
		t.Fatalf("protocol failure accounting=%+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoverageUnavailable ||
			summary.CurrentReason != coverage.ReasonCleanupUnproved ||
			summary.DroppedEventCount != 1 {
			t.Fatalf("%s protocol-exit coverage=%+v", subsystem, summary)
		}
		intervals := snapshot.Intervals[subsystem]
		if len(intervals) != 4 ||
			intervals[1].Reason != coverage.ReasonInvalidFrame ||
			intervals[2].Reason != coverage.ReasonDaemonDisconnected {
			t.Fatalf("%s protocol failure interval=%+v", subsystem, intervals)
		}
	}
}

func TestActivityServiceAccountsPreReadyObservationFailureOnSessionExit(t *testing.T) {
	service := newActivityService(nil)
	preparation := activityPreparationFixture(t)
	if _, err := service.Prepare(preparation); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 779, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.SessionExited(preparation.SessionID, nil, sessionwire.ErrObserverSchema); err != nil {
		t.Fatal(err)
	}

	snapshot, _ := service.Snapshot(preparation.SessionID)
	if snapshot.Invalid != 1 || !snapshot.SessionClosed {
		t.Fatalf("pre-ready session failure accounting=%+v", snapshot)
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.CurrentReason != coverage.ReasonCleanupUnproved ||
			summary.DroppedEventCount != 1 {
			t.Fatalf("%s pre-ready failure coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceDegradesCoverageWhenRedactionSnapshotIsUnavailable(t *testing.T) {
	service := newActivityService(nil)
	service.setRedactionBuilder(activityRedactionBuilderFunc(
		func(context.Context) (*workloadredact.Snapshot, error) {
			return nil, workloadredact.ErrSnapshotUnavailable
		},
	))
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	if expectation.RedactionGeneration != "" {
		t.Fatalf("failed redaction snapshot advertised generation %q",
			expectation.RedactionGeneration)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 780, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity session is missing")
	}
	for _, subsystem := range activitySubsystems {
		summary := snapshot.Coverage[subsystem]
		if summary.CurrentState != workloadtypes.CoveragePartial ||
			summary.CurrentReason != coverage.ReasonRedactionDropped ||
			summary.DroppedEventCount != 1 {
			t.Fatalf("%s redaction coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceBoundsAndSingleFlightsBlockedRedactionBuild(t *testing.T) {
	service := newActivityService(nil)
	service.redactionWait = 20 * time.Millisecond
	release := make(chan struct{})
	var callsMu sync.Mutex
	calls := 0
	service.setRedactionBuilder(activityRedactionBuilderFunc(
		func(context.Context) (*workloadredact.Snapshot, error) {
			callsMu.Lock()
			calls++
			callsMu.Unlock()
			<-release
			return (workloadredact.Builder{}).Build(
				context.Background(),
			)
		},
	))
	first := activityPreparationFixture(t)
	expectation, err := service.Prepare(first)
	if err != nil {
		t.Fatal(err)
	}
	if expectation.RedactionGeneration != "" {
		t.Fatalf("timed-out build advertised %q",
			expectation.RedactionGeneration)
	}
	second := first
	second.SessionID = "ses_activity_second"
	second.ObserverGeneration++
	expectation, err = service.Prepare(second)
	if err != nil {
		t.Fatal(err)
	}
	if expectation.RedactionGeneration != "" {
		t.Fatalf("blocked single-flight advertised %q",
			expectation.RedactionGeneration)
	}
	callsMu.Lock()
	gotCalls := calls
	callsMu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("blocked redaction builds=%d want 1", gotCalls)
	}
	close(release)
}

func TestActivityServiceSerializesPrepareAcrossSecretMutation(t *testing.T) {
	service := newActivityService(nil)
	firstBuildStarted := make(chan struct{})
	releaseFirstBuild := make(chan struct{})
	var buildMu sync.Mutex
	buildCalls := 0
	service.setRedactionBuilder(activityRedactionBuilderFunc(
		func(ctx context.Context) (*workloadredact.Snapshot, error) {
			buildMu.Lock()
			buildCalls++
			call := buildCalls
			buildMu.Unlock()
			if call == 1 {
				close(firstBuildStarted)
				select {
				case <-releaseFirstBuild:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return (workloadredact.Builder{}).Build(ctx)
		},
	))

	type prepareResult struct {
		expectation sessionwire.SupervisorActivityExpectation
		err         error
	}
	first := activityPreparationFixture(t)
	firstDone := make(chan prepareResult, 1)
	go func() {
		expectation, err := service.Prepare(first)
		firstDone <- prepareResult{expectation: expectation, err: err}
	}()
	<-firstBuildStarted

	type mutationResult struct {
		release func()
		err     error
	}
	mutationDone := make(chan mutationResult, 1)
	go func() {
		release, err := service.beginSecretMutation()
		mutationDone <- mutationResult{release: release, err: err}
	}()
	select {
	case result := <-mutationDone:
		if result.release != nil {
			result.release()
		}
		t.Fatal("secret mutation crossed an in-flight snapshot build")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseFirstBuild)
	firstResult := <-firstDone
	if firstResult.err != nil {
		t.Fatal(firstResult.err)
	}
	if firstResult.expectation.RedactionGeneration == "" {
		t.Fatal("first Prepare did not build a redaction snapshot")
	}
	mutation := <-mutationDone
	if mutation.err != nil {
		t.Fatal(mutation.err)
	}
	if mutation.release == nil {
		t.Fatal("secret mutation did not return a release function")
	}
	snapshot, ok := service.Snapshot(first.SessionID)
	if !ok || snapshot.RedactionGeneration != "" {
		t.Fatalf("pre-mutation snapshot was not invalidated: %+v", snapshot)
	}

	second := first
	second.SessionID = "ses_activity_after_mutation"
	second.ObserverGeneration++
	secondDone := make(chan prepareResult, 1)
	go func() {
		expectation, err := service.Prepare(second)
		secondDone <- prepareResult{expectation: expectation, err: err}
	}()
	select {
	case result := <-secondDone:
		t.Fatalf("Prepare crossed an active secret mutation: %+v", result)
	case <-time.After(30 * time.Millisecond):
	}
	mutation.release()
	mutation.release()
	secondResult := <-secondDone
	if secondResult.err != nil {
		t.Fatal(secondResult.err)
	}
	if secondResult.expectation.RedactionGeneration == "" {
		t.Fatal("post-mutation Prepare did not build a fresh snapshot")
	}
}

func TestActivityServiceFreezesRedactionSnapshotAndInvalidatesBeforeSecretMutation(t *testing.T) {
	now := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root:               filepath.Join(t.TempDir(), "activity"),
		ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes:      8 << 20,
		GlobalBytes:        32 << 20,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	controls := &activityControlTokenSource{
		token: workloadredact.ControlToken{
			ID: "daemon-operator", Generation: 5,
			Value: []byte("token_activity_redaction_fixture_045"),
		},
	}
	builder := workloadredact.Builder{
		ControlTokens: controls,
		Now:           func() time.Time { return now },
	}
	builds := 0
	service := newActivityService(func() time.Time { return now })
	service.setPersistentStore(persistent)
	service.setRedactionBuilder(activityRedactionBuilderFunc(
		func(ctx context.Context) (*workloadredact.Snapshot, error) {
			builds++
			return builder.Build(ctx)
		},
	))
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(expectation.RedactionGeneration, "red_") {
		t.Fatalf("redaction generation=%q", expectation.RedactionGeneration)
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 781, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     "act_redactionpersist1",
		Owner:  preparation.Owner, SessionID: preparation.SessionID,
		Kind: workloadtypes.ActivityProcess, Operation: "exec",
		Subject: workloadtypes.ProcessSubject{
			Kind:        workloadtypes.ActivityProcess,
			ExecutionID: "exec_redactionpersist1",
			Executable:  "/usr/bin/agent",
			Argv: []string{
				"agent", "--header",
				"token_activity_redaction_fixture_045",
			},
			Cwd: "/workspace",
			GuestIdentity: workloadtypes.GuestIdentity{
				UID: 1000, GID: 1000,
			},
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: now, LastAt: now,
		FirstSequence: 1, LastSequence: 1,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_redactionpersist1",
		RedactionStatus: workloadtypes.RedactionPending,
	}
	if err := service.PersistActivity(
		context.Background(),
		preparation.SessionID,
		record,
	); err != nil {
		t.Fatal(err)
	}
	stored, err := persistent.Snapshot(
		context.Background(),
		preparation.Owner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Records) != 1 ||
		stored.Records[0].RedactionStatus != workloadtypes.RedactionPassed {
		t.Fatalf("stored records=%+v", stored.Records)
	}
	encoded, err := json.Marshal(stored.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(
		string(encoded),
		"token_activity_redaction_fixture_045",
	) {
		t.Fatalf("persisted activity retained control token: %s", encoded)
	}
	if builds != 1 {
		t.Fatalf("redaction snapshot builds=%d want one per session", builds)
	}

	service.setRedactionBuilder(workloadredact.Builder{
		ControlTokens: controls,
		Now:           func() time.Time { return now },
		MaxArguments:  1,
	})
	stillFrozen := record
	stillFrozen.ID = "act_redactionfrozen2"
	stillFrozen.FirstSequence = 2
	stillFrozen.LastSequence = 2
	stillFrozen.Subject = workloadtypes.ProcessSubject{
		Kind:        workloadtypes.ActivityProcess,
		ExecutionID: "exec_redactionfrozen2",
		Executable:  "/usr/bin/agent",
		Argv:        []string{"agent", "ordinary", "too-many"},
		Cwd:         "/workspace",
		GuestIdentity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000,
		},
	}
	if err := service.PersistActivity(
		context.Background(),
		preparation.SessionID,
		stillFrozen,
	); err != nil {
		t.Fatalf("frozen session snapshot changed after builder replacement: %v", err)
	}
	if builds != 1 {
		t.Fatalf("record persistence rebuilt redaction snapshot %d times", builds)
	}
	if err := service.invalidateRedactionSnapshots(); err != nil {
		t.Fatal(err)
	}
	rejected := stillFrozen
	rejected.ID = "act_redactionrejected3"
	rejected.FirstSequence = 3
	rejected.LastSequence = 3
	rejected.Subject = workloadtypes.ProcessSubject{
		Kind:        workloadtypes.ActivityProcess,
		ExecutionID: "exec_redactionrejected3",
		Executable:  "/usr/bin/agent",
		Argv:        []string{"agent", "ordinary", "too-many"},
		Cwd:         "/workspace",
		GuestIdentity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000,
		},
	}
	if err := service.PersistActivity(
		context.Background(),
		preparation.SessionID,
		rejected,
	); !errors.Is(err, workloadredact.ErrSnapshotUnavailable) {
		t.Fatalf("post-mutation redaction refusal error=%v", err)
	}
	stored, err = persistent.Snapshot(context.Background(), preparation.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Records) != 2 {
		t.Fatalf("failed-redaction record reached persistence: %+v",
			stored.Records)
	}
	live, ok := service.Snapshot(preparation.SessionID)
	if !ok {
		t.Fatal("activity session is missing")
	}
	for _, subsystem := range activitySubsystems {
		summary := live.Coverage[subsystem]
		wantDropped := uint64(1)
		if subsystem == workloadtypes.SubsystemProcess {
			wantDropped = 2
		}
		if summary.CurrentReason != coverage.ReasonRedactionDropped ||
			summary.DroppedEventCount != wantDropped {
			t.Fatalf("%s mutation invalidation coverage=%+v",
				subsystem, summary)
		}
	}
}

func TestActivityServiceRefreshesRedactionBeforeLiveIngestResumes(
	t *testing.T,
) {
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root:               filepath.Join(t.TempDir(), "activity"),
		ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes:      8 << 20,
		GlobalBytes:        32 << 20,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	controls := &activityControlTokenSource{
		token: workloadredact.ControlToken{
			ID:         "daemon-operator",
			Generation: 1,
			Value:      []byte("token_rotation_old_0123456789abcdef"),
		},
	}
	builds := 0
	service := newActivityService(func() time.Time { return now })
	service.setPersistentStore(persistent)
	service.setRedactionBuilder(activityRedactionBuilderFunc(
		func(ctx context.Context) (*workloadredact.Snapshot, error) {
			builds++
			return (workloadredact.Builder{
				ControlTokens: controls,
				Now:           func() time.Time { return now },
			}).Build(ctx)
		},
	))
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(expectation.ObserverStreamToken.Destroy)
	initialGeneration := expectation.RedactionGeneration
	if initialGeneration == "" {
		t.Fatal("initial redaction generation is empty")
	}
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 784, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	release, err := service.beginSecretMutation()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	duringMutation, ok := service.Snapshot(preparation.SessionID)
	if !ok || duringMutation.RedactionGeneration != "" {
		t.Fatalf("mutation did not invalidate the old snapshot: %+v", duringMutation)
	}
	const rotatedToken = "token_rotation_new_0123456789abcdef"
	controls.token = workloadredact.ControlToken{
		ID:         "daemon-operator",
		Generation: 2,
		Value:      []byte(rotatedToken),
	}
	record := activityProcessRecord(
		preparation,
		now,
		1,
		"act_live_rotation_1",
	)
	subject := record.Subject.(workloadtypes.ProcessSubject)
	subject.ExecutionID = "exec_live_rotation_1"
	subject.Argv = []string{"agent", "--token", rotatedToken}
	record.Subject = subject
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	ingestDone := make(chan error, 1)
	go func() {
		ingestDone <- service.Ingest(
			preparation.SessionID,
			activityEnvelope(
				preparation,
				784,
				9,
				1,
				"process.exec",
				string(payload),
			),
		)
	}()
	select {
	case ingestErr := <-ingestDone:
		t.Fatalf("ingest crossed the active secret mutation: %v", ingestErr)
	case <-time.After(30 * time.Millisecond):
	}

	release()
	if err := <-ingestDone; err != nil {
		t.Fatal(err)
	}
	afterMutation, ok := service.Snapshot(preparation.SessionID)
	if !ok ||
		afterMutation.RedactionGeneration == "" ||
		afterMutation.RedactionGeneration == initialGeneration ||
		afterMutation.ObserverClosed {
		t.Fatalf("live snapshot refresh state=%+v", afterMutation)
	}
	if builds != 2 {
		t.Fatalf("redaction snapshot builds=%d want initial plus refresh", builds)
	}
	stored, err := persistent.Snapshot(
		context.Background(),
		preparation.Owner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Records) != 1 {
		t.Fatalf("stored records=%d want 1", len(stored.Records))
	}
	encoded, err := json.Marshal(stored.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), rotatedToken) ||
		!strings.Contains(string(encoded), workloadredact.Replacement) {
		t.Fatalf("rotated token was not redacted before persistence: %s", encoded)
	}
	for _, subsystem := range activitySubsystems {
		summary := afterMutation.Coverage[subsystem]
		if summary.CurrentReason != coverage.ReasonRedactionDropped ||
			summary.DroppedEventCount < 1 {
			t.Fatalf("%s mutation coverage=%+v", subsystem, summary)
		}
	}
}

func TestActivityServiceKeepsObserverOpenWhenLiveRefreshFails(
	t *testing.T,
) {
	now := time.Date(2026, 7, 30, 15, 10, 0, 0, time.UTC)
	builds := 0
	service := newActivityService(func() time.Time { return now })
	service.setRedactionBuilder(activityRedactionBuilderFunc(
		func(ctx context.Context) (*workloadredact.Snapshot, error) {
			builds++
			if builds > 1 {
				return nil, workloadredact.ErrSnapshotUnavailable
			}
			return (workloadredact.Builder{
				Now: func() time.Time { return now },
			}).Build(ctx)
		},
	))
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(expectation.ObserverStreamToken.Destroy)
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 785, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}
	release, err := service.beginSecretMutation()
	if err != nil {
		t.Fatal(err)
	}
	release()

	record := activityProcessRecord(
		preparation,
		now,
		1,
		"act_failed_refresh_1",
	)
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(
		preparation.SessionID,
		activityEnvelope(
			preparation,
			785,
			10,
			1,
			"process.exec",
			string(payload),
		),
	); err != nil {
		t.Fatalf("safe redaction drop closed the observer stream: %v", err)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || snapshot.ObserverClosed ||
		snapshot.RedactionGeneration != "" ||
		snapshot.Accepted != 0 {
		t.Fatalf("failed refresh state=%+v", snapshot)
	}
	processCoverage := snapshot.Coverage[workloadtypes.SubsystemProcess]
	if processCoverage.CurrentReason != coverage.ReasonRedactionDropped ||
		processCoverage.DroppedEventCount < 2 {
		t.Fatalf("failed refresh coverage=%+v", processCoverage)
	}
}

func TestSessionWorkerRefusesStartedUntilRequiredBoundaryIsRegistered(t *testing.T) {
	registry := newSessionRegistry(1, nil)
	worker, err := registry.register("conn_activity", func() {})
	if err != nil {
		t.Fatal(err)
	}
	streams := worker.activityStreams()
	preparation := activityPreparationFixture(t)
	expectation, err := streams.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	start := sessionStart{
		SessionID: preparation.SessionID, EnvironmentID: preparation.EnvironmentID,
		Profile: "default", Backend: "lima", TerminalMode: "none",
		SessionSnapshotID: "sha256:" + strings.Repeat("b", 64), CommandClass: "claude",
	}
	if err := worker.markStarted(start); !errors.Is(err, errActivityBoundaryNotReady) {
		t.Fatalf("pre-boundary start error=%v", err)
	}
	ready := activityReadyFixture(preparation, 444, allAvailableCoverage())
	if err := streams.BoundaryReady(ready); err != nil {
		t.Fatal(err)
	}
	if err := worker.markStarted(start); err != nil {
		t.Fatal(err)
	}
	expectation.ObserverStreamToken.Destroy()
	registry.finish("conn_activity", "")
	snapshot, ok := registry.activity.Snapshot(preparation.SessionID)
	if !ok || !snapshot.SessionClosed {
		t.Fatalf("registry exit did not close activity: present=%v snapshot=%+v", ok, snapshot)
	}
}

func TestActivityServiceIngestsStrictGuestRecordWithHostCoverageAndRedaction(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 14, 30, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root:               filepath.Join(t.TempDir(), "activity"),
		ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes:      8 << 20,
		GlobalBytes:        32 << 20,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })

	service := newActivityService(func() time.Time { return now })
	service.setPersistentStore(persistent)
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(expectation.ObserverStreamToken.Destroy)
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 782, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	record := activityProcessRecord(
		preparation,
		now,
		1,
		"act_observerpersist1",
	)
	record.Subject = workloadtypes.ProcessSubject{
		Kind:        workloadtypes.ActivityProcess,
		ExecutionID: "exec_observerpersist1",
		Executable:  "/usr/bin/agent",
		Argv: []string{
			"agent",
			"--proxy=socks5://operator:private-password@127.0.0.1:7890",
		},
		Cwd: "/workspace",
		GuestIdentity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000,
		},
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	envelope := activityEnvelope(
		preparation,
		782,
		3,
		1,
		"process.exec",
		string(payload),
	)
	if err := service.Ingest(preparation.SessionID, envelope); err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(preparation.SessionID, envelope); err != nil {
		t.Fatal(err)
	}

	stored, err := persistent.Snapshot(
		context.Background(),
		preparation.Owner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Records) != 1 {
		t.Fatalf("stored records=%d want one after duplicate", len(stored.Records))
	}
	got := stored.Records[0]
	if got.RedactionStatus != workloadtypes.RedactionPassed ||
		got.CoverageID == record.CoverageID ||
		!strings.HasPrefix(got.CoverageID, "cov_") {
		t.Fatalf("host-owned persisted record=%+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-password") ||
		strings.Contains(string(encoded), "operator:") {
		t.Fatalf("observer record bypassed host redaction: %s", encoded)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || snapshot.Accepted != 1 || snapshot.Duplicates != 1 {
		t.Fatalf("ingest accounting=%+v", snapshot)
	}

	unknownTop := strings.Replace(
		string(payload),
		`"operation":"exec"`,
		`"operation":"exec","unexpected":true`,
		1,
	)
	if _, err := decodeObservedActivity(
		json.RawMessage(unknownTop),
	); !errors.Is(err, errActivityObservationInvalid) {
		t.Fatalf("unknown record field error=%v", err)
	}
	unknownSubject := strings.Replace(
		string(payload),
		`"subject":{"kind":"process",`,
		`"subject":{"kind":"process","unexpected":true,`,
		1,
	)
	if _, err := decodeObservedActivity(
		json.RawMessage(unknownSubject),
	); !errors.Is(err, errActivityObservationInvalid) {
		t.Fatalf("unknown subject field error=%v", err)
	}
}

func TestActivityServicePersistsRedactedExecutionUpdates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 14, 45, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root:               filepath.Join(t.TempDir(), "activity"),
		ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes:      8 << 20,
		GlobalBytes:        32 << 20,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })

	service := newActivityService(func() time.Time { return now })
	service.setPersistentStore(persistent)
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(expectation.ObserverStreamToken.Destroy)
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 784, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	identity := workloadtypes.ExecutionIdentityInput{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		GuestBootID:        preparation.GuestBootID,
		ObserverGeneration: preparation.ObserverGeneration,
		PID:                4242, ExecSequence: 7, StartedAtMonoNS: 101,
	}
	executionID, err := workloadtypes.NewExecutionID(identity)
	if err != nil {
		t.Fatal(err)
	}
	execution := workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: executionID,
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		GuestBootID:        preparation.GuestBootID,
		ObserverGeneration: preparation.ObserverGeneration,
		PID:                identity.PID, TID: identity.PID,
		ExecSequence:    identity.ExecSequence,
		StartedAtMonoNS: identity.StartedAtMonoNS, StartedAt: now,
		Executable: "/usr/bin/agent",
		Argv: []string{
			"agent", "--token", "execution-private-value",
			"/Users/alice/projects/visible-local-path",
		},
		Cwd: "/Users/alice/projects/visible-local-path",
		Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "developer",
		},
		Limitations: []string{},
	}
	payload, err := json.Marshal(execution)
	if err != nil {
		t.Fatal(err)
	}
	started := activityEnvelope(
		preparation,
		784,
		4,
		1,
		"process.execution",
		string(payload),
	)
	if err := service.Ingest(preparation.SessionID, started); err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(preparation.SessionID, started); err != nil {
		t.Fatal(err)
	}

	exitCode := 0
	execution.Exit = &workloadtypes.ExitObservation{
		Code: &exitCode, AtMonoNS: 102, At: now.Add(time.Second),
	}
	payload, err = json.Marshal(execution)
	if err != nil {
		t.Fatal(err)
	}
	exited := activityEnvelope(
		preparation,
		784,
		4,
		2,
		"process.execution",
		string(payload),
	)
	if err := service.Ingest(preparation.SessionID, exited); err != nil {
		t.Fatal(err)
	}

	stored, err := persistent.Snapshot(
		context.Background(),
		preparation.Owner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Executions) != 1 ||
		stored.Executions[0].ID != execution.ID ||
		stored.Executions[0].Exit == nil ||
		stored.Executions[0].Exit.Code == nil ||
		*stored.Executions[0].Exit.Code != 0 {
		t.Fatalf("stored execution updates=%+v", stored.Executions)
	}
	encoded, err := json.Marshal(stored.Executions[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "execution-private-value") ||
		!strings.Contains(
			string(encoded),
			"/Users/alice/projects/visible-local-path",
		) {
		t.Fatalf("execution redaction/path visibility=%s", encoded)
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || snapshot.Accepted != 2 || snapshot.Duplicates != 1 {
		t.Fatalf("execution ingest accounting=%+v", snapshot)
	}
}

func TestActivityServiceIngestBatchRedactsAndDurablyGroupsRecords(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 14, 45, 0, 0, time.UTC)
	persistent, err := workloadstore.Open(workloadstore.Options{
		Root:               filepath.Join(t.TempDir(), "activity"),
		ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes:      8 << 20,
		GlobalBytes:        32 << 20,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })

	service := newActivityService(func() time.Time { return now })
	service.setPersistentStore(persistent)
	preparation := activityPreparationFixture(t)
	expectation, err := service.Prepare(preparation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(expectation.ObserverStreamToken.Destroy)
	if err := service.RegisterBoundary(
		preparation.SessionID,
		activityReadyFixture(preparation, 783, allAvailableCoverage()),
	); err != nil {
		t.Fatal(err)
	}

	session, err := service.session(preparation.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	persist := session.persistActivities
	persistCalls := 0
	persistedRecords := 0
	session.persistActivities = func(
		ctx context.Context,
		records []workloadtypes.ActivityRecord,
	) error {
		persistCalls++
		persistedRecords += len(records)
		return persist(ctx, records)
	}
	session.mu.Unlock()

	const recordCount = 64
	envelopes := make([]sessionwire.ObservationEnvelope, recordCount)
	for index := range envelopes {
		sequence := uint64(index + 1)
		record := activityProcessRecord(
			preparation,
			now.Add(time.Duration(index)*time.Millisecond),
			sequence,
			fmt.Sprintf("act_observerbatch%04d", index),
		)
		subject := record.Subject.(workloadtypes.ProcessSubject)
		subject.ExecutionID = fmt.Sprintf("exec_observerbatch%04d", index)
		subject.Argv = []string{
			"agent",
			"--token",
			"batch-secret",
		}
		record.Subject = subject
		payload, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		envelopes[index] = activityEnvelope(
			preparation,
			783,
			4,
			sequence,
			"process.exec",
			string(payload),
		)
	}
	if err := service.IngestBatch(preparation.SessionID, envelopes); err != nil {
		t.Fatal(err)
	}
	if persistCalls != 1 || persistedRecords != recordCount {
		t.Fatalf(
			"durable batch calls=%d records=%d",
			persistCalls,
			persistedRecords,
		)
	}

	stored, err := persistent.Snapshot(context.Background(), preparation.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Records) != recordCount {
		t.Fatalf("stored batch records=%d want=%d", len(stored.Records), recordCount)
	}
	for _, record := range stored.Records {
		subject := record.Subject.(workloadtypes.ProcessSubject)
		if len(subject.Argv) != 3 ||
			subject.Argv[2] != workloadredact.Replacement {
			t.Fatalf("batch record was not redacted: %+v", subject.Argv)
		}
	}
	snapshot, ok := service.Snapshot(preparation.SessionID)
	if !ok || snapshot.Accepted != recordCount {
		t.Fatalf("batch accounting=%+v", snapshot)
	}
}

func activityProcessRecord(
	preparation backend.ActivityPreparation,
	at time.Time,
	sequence uint64,
	id string,
) workloadtypes.ActivityRecord {
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     id,
		Owner:  preparation.Owner, SessionID: preparation.SessionID,
		Kind: workloadtypes.ActivityProcess, Operation: "exec",
		Subject: workloadtypes.ProcessSubject{
			Kind:        workloadtypes.ActivityProcess,
			ExecutionID: "exec_activityfixture1",
			Executable:  "/usr/bin/true",
			Argv:        []string{"true"},
			Cwd:         "/workspace",
			GuestIdentity: workloadtypes.GuestIdentity{
				UID: 1000, GID: 1000,
			},
		},
		Outcome: workloadtypes.Outcome{
			Status: workloadtypes.OutcomeSucceeded,
		},
		Count: 1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_observerpending1",
		RedactionStatus: workloadtypes.RedactionPending,
	}
}

type activityRedactionBuilderFunc func(
	context.Context,
) (*workloadredact.Snapshot, error)

func (builder activityRedactionBuilderFunc) Build(
	ctx context.Context,
) (*workloadredact.Snapshot, error) {
	return builder(ctx)
}

type activityControlTokenSource struct {
	token workloadredact.ControlToken
}

func (source *activityControlTokenSource) SnapshotControlTokens(
	context.Context,
) ([]workloadredact.ControlToken, error) {
	token := source.token
	token.Value = append([]byte(nil), source.token.Value...)
	return []workloadredact.ControlToken{token}, nil
}

func activityPreparationFixture(t *testing.T) backend.ActivityPreparation {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		"env_activity",
		"lima",
		"hideout-activity:7:01234567-89ab-cdef-0123-456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	return backend.ActivityPreparation{
		Owner: owner, SessionID: "ses_activity",
		EnvironmentID: "env_activity", Backend: "lima",
		BackendIncarnationID: owner.BackendIncarnationID,
		GuestBootID:          "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration:   1,
		ObserverHelperDigest: "sha256:" + strings.Repeat("a", 64),
		Retention:            workloadtypes.DefaultActivityRetentionPolicy(),
	}
}

func activityReadyFixture(
	preparation backend.ActivityPreparation,
	cgroupID uint64,
	summaries []sessionwire.SupervisorCoverageSummary,
) *sessionwire.SupervisorActivityReady {
	return &sessionwire.SupervisorActivityReady{
		Boundary: workloadtypes.WorkloadBoundary{
			Schema: workloadtypes.WorkloadBoundarySchema,
			Owner:  preparation.Owner, SessionID: preparation.SessionID,
			CgroupPath: "/sys/fs/cgroup/hideout/" + preparation.SessionID,
			CgroupID:   cgroupID, TargetUser: "developer",
			State:              workloadtypes.BoundaryReady,
			ObserverGeneration: preparation.ObserverGeneration,
			GuestBootID:        preparation.GuestBootID, CreatedAtMonoNS: 100,
		},
		ObserverHelperDigest: preparation.ObserverHelperDigest,
		Coverage:             summaries,
	}
}

func allAvailableCoverage() []sessionwire.SupervisorCoverageSummary {
	return []sessionwire.SupervisorCoverageSummary{
		{Subsystem: workloadtypes.SubsystemProcess, State: workloadtypes.CoverageAvailable, Reason: "collector-ready", Evidence: []string{"tracepoint.exec"}},
		{Subsystem: workloadtypes.SubsystemFile, State: workloadtypes.CoverageAvailable, Reason: "collector-ready", Evidence: []string{"fentry.vfs"}},
		{Subsystem: workloadtypes.SubsystemNetwork, State: workloadtypes.CoverageAvailable, Reason: "collector-ready", Evidence: []string{"cgroup.connect4"}},
		{Subsystem: workloadtypes.SubsystemDNS, State: workloadtypes.CoverageAvailable, Reason: "collector-ready", Evidence: []string{"socket-cookie"}},
	}
}

func activityEnvelope(
	preparation backend.ActivityPreparation,
	cgroupID, cpu, sequence uint64,
	kind, payload string,
) sessionwire.ObservationEnvelope {
	return sessionwire.ObservationEnvelope{
		Schema: sessionwire.ObservationSchema, Owner: preparation.Owner,
		SessionID: preparation.SessionID, CgroupID: cgroupID,
		ObserverGeneration: preparation.ObserverGeneration,
		CPU:                cpu, Sequence: sequence, MonotonicNS: 100 + sequence,
		Kind: kind, Payload: json.RawMessage(payload),
	}
}
