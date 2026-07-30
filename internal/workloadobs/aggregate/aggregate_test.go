package aggregate_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/workloadobs/aggregate"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestAggregatorMergesOnlyExactOwnerExecutionCoverageAndWindow(t *testing.T) {
	base := aggregateFileRecord(t, "act_fixture0001", "ses_aggregate_a", "exec_fixture0001", "cov_file000001")
	base.FirstAt = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	base.LastAt = base.FirstAt
	base.FirstSequence, base.LastSequence = 10, 10
	base.Count, base.Bytes = 1, 10
	second := base
	second.ID = "act_fixture0002"
	second.FirstAt, second.LastAt = base.FirstAt.Add(time.Second), base.FirstAt.Add(time.Second)
	second.FirstSequence, second.LastSequence = 11, 11
	second.Count, second.Bytes = 2, 20

	aggregator, err := aggregate.New(aggregate.Options{Window: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Add(base); err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Add(second); err != nil {
		t.Fatal(err)
	}
	// Delivery retry of the same evidence ID must not double-count.
	if err := aggregator.Add(second); err != nil {
		t.Fatal(err)
	}
	records := aggregator.Records()
	if len(records) != 1 {
		t.Fatalf("record count=%d want 1: %+v", len(records), records)
	}
	merged := records[0]
	if merged.Count != 3 || merged.Bytes != 30 ||
		merged.FirstSequence != 10 || merged.LastSequence != 11 ||
		!merged.FirstAt.Equal(base.FirstAt) || !merged.LastAt.Equal(second.LastAt) {
		t.Fatalf("merged record=%+v", merged)
	}

	isolationCases := []struct {
		name   string
		mutate func(*workloadtypes.ActivityRecord)
	}{
		{
			name: "session",
			mutate: func(record *workloadtypes.ActivityRecord) {
				record.SessionID = "ses_aggregate_b"
			},
		},
		{
			name: "execution",
			mutate: func(record *workloadtypes.ActivityRecord) {
				record.Actor.ExecutionID = "exec_fixture0002"
			},
		},
		{
			name: "coverage",
			mutate: func(record *workloadtypes.ActivityRecord) {
				record.CoverageID = "cov_file000002"
			},
		},
		{
			name: "attribution",
			mutate: func(record *workloadtypes.ActivityRecord) {
				record.Attribution = workloadtypes.AttributionInferred
			},
		},
		{
			name: "outside-window",
			mutate: func(record *workloadtypes.ActivityRecord) {
				record.FirstAt = base.FirstAt.Add(6 * time.Second)
				record.LastAt = record.FirstAt
			},
		},
	}
	for index, testCase := range isolationCases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := base
			candidate.Actor = cloneActor(base.Actor)
			candidate.ID = "act_isolated00" + string(rune('1'+index))
			candidate.FirstSequence = uint64(20 + index)
			candidate.LastSequence = candidate.FirstSequence
			testCase.mutate(&candidate)
			if err := aggregator.Add(candidate); err != nil {
				t.Fatal(err)
			}
		})
	}
	if got := len(aggregator.Records()); got != 1+len(isolationCases) {
		t.Fatalf("isolated records merged: got=%d records=%+v", got, aggregator.Records())
	}
}

func TestAggregatorNeverCollapsesDestructiveFileEvidence(t *testing.T) {
	first := aggregateFileRecord(t, "act_destructive01", "ses_aggregate_a", "exec_fixture0001", "cov_file000001")
	first.Operation = "unlink"
	subject := first.Subject.(workloadtypes.FileSubject)
	subject.Destructive = true
	first.Subject = subject
	second := first
	second.ID = "act_destructive02"
	second.FirstAt = first.FirstAt.Add(time.Millisecond)
	second.LastAt = second.FirstAt
	second.FirstSequence, second.LastSequence = 2, 2

	aggregator, err := aggregate.New(aggregate.Options{Window: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []workloadtypes.ActivityRecord{second, first} {
		if err := aggregator.Add(record); err != nil {
			t.Fatal(err)
		}
	}
	records := aggregator.Records()
	if len(records) != 2 || records[0].Count != 1 || records[1].Count != 1 {
		t.Fatalf("destructive events were collapsed: %+v", records)
	}
}

func TestAggregatorOutputIsDeterministicAcrossArrivalOrder(t *testing.T) {
	first := aggregateFileRecord(t, "act_fixture0001", "ses_aggregate_a", "exec_fixture0001", "cov_file000001")
	second := first
	second.ID = "act_fixture0002"
	second.FirstAt = first.FirstAt.Add(time.Second)
	second.LastAt = second.FirstAt
	second.FirstSequence, second.LastSequence = 2, 2

	build := func(input []workloadtypes.ActivityRecord) []workloadtypes.ActivityRecord {
		t.Helper()
		value, err := aggregate.New(aggregate.Options{Window: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range input {
			if err := value.Add(record); err != nil {
				t.Fatal(err)
			}
		}
		return value.Records()
	}
	forward := build([]workloadtypes.ActivityRecord{first, second})
	reverse := build([]workloadtypes.ActivityRecord{second, first})
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("arrival order changed aggregate\nforward=%+v\nreverse=%+v", forward, reverse)
	}
}

func TestAggregatorMergesNetworkAndDNSWithinExactSemanticWindow(t *testing.T) {
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	networkFirst := aggregateNetworkRecord(t, "act_network001", base, 1, 1001)
	networkSecond := aggregateNetworkRecord(t, "act_network002", base.Add(2*time.Second), 2, 1002)
	dnsFirst := aggregateDNSRecord(t, "act_dnsrecord01", base, 3)
	dnsSecond := aggregateDNSRecord(t, "act_dnsrecord02", base.Add(2*time.Second), 4)

	aggregator, err := aggregate.New(aggregate.Options{Window: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []workloadtypes.ActivityRecord{
		networkSecond,
		dnsSecond,
		networkFirst,
		dnsFirst,
	} {
		if err := aggregator.Add(record); err != nil {
			t.Fatal(err)
		}
	}
	records := aggregator.Records()
	if len(records) != 2 {
		t.Fatalf("record count=%d want 2: %+v", len(records), records)
	}
	var network, dns *workloadtypes.ActivityRecord
	for index := range records {
		switch records[index].Kind {
		case workloadtypes.ActivityConnection:
			network = &records[index]
		case workloadtypes.ActivityDNS:
			dns = &records[index]
		}
	}
	if network == nil || network.Count != 2 || network.Bytes != 20 {
		t.Fatalf("network aggregate=%+v", network)
	}
	networkSubject, ok := network.Subject.(workloadtypes.NetworkSubject)
	if !ok || networkSubject.SocketCookie != 0 ||
		!slices.Contains(network.Truncation, "socket-cookie-aggregated") {
		t.Fatalf("network aggregate retained a fabricated socket identity: %+v", network)
	}
	if dns == nil || dns.Count != 2 || dns.FirstSequence != 3 || dns.LastSequence != 4 {
		t.Fatalf("dns aggregate=%+v", dns)
	}
}

func TestAggregatorPreservesProcessLifecycleAndDestructiveFileEvidence(t *testing.T) {
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	processFirst := aggregateProcessRecord(t, "act_process001", base, 1)
	processSecond := aggregateProcessRecord(t, "act_process002", base.Add(time.Millisecond), 2)
	destructiveFirst := aggregateFileRecord(
		t,
		"act_destroy0001",
		"ses_aggregate_a",
		"exec_fixture0001",
		"cov_file000001",
	)
	destructiveFirst.Operation = "rename"
	fileSubject := destructiveFirst.Subject.(workloadtypes.FileSubject)
	fileSubject.Destructive = true
	fileSubject.TargetPath = "/workspace/renamed.txt"
	destructiveFirst.Subject = fileSubject
	destructiveSecond := destructiveFirst
	destructiveSecond.ID = "act_destroy0002"
	destructiveSecond.FirstAt = destructiveFirst.FirstAt.Add(time.Millisecond)
	destructiveSecond.LastAt = destructiveSecond.FirstAt
	destructiveSecond.FirstSequence = 3
	destructiveSecond.LastSequence = 3

	aggregator, err := aggregate.New(aggregate.Options{Window: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []workloadtypes.ActivityRecord{
		processFirst,
		processSecond,
		destructiveFirst,
		destructiveSecond,
	} {
		if err := aggregator.Add(record); err != nil {
			t.Fatal(err)
		}
	}
	records := aggregator.Records()
	if len(records) != 4 {
		t.Fatalf("lifecycle/destructive evidence collapsed: %+v", records)
	}
	for _, record := range records {
		if record.Count != 1 {
			t.Fatalf("non-aggregatable evidence count=%d: %+v", record.Count, record)
		}
	}
}

func TestAggregatorWindowIsInclusiveAnchoredAndDoesNotChainForever(t *testing.T) {
	base := aggregateFileRecord(t, "act_window0001", "ses_aggregate_a", "exec_fixture0001", "cov_file000001")
	base.FirstAt = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	base.LastAt = base.FirstAt
	atBoundary := base
	atBoundary.ID = "act_window0002"
	atBoundary.FirstAt = base.FirstAt.Add(5 * time.Second)
	atBoundary.LastAt = atBoundary.FirstAt
	atBoundary.FirstSequence, atBoundary.LastSequence = 2, 2
	chained := base
	chained.ID = "act_window0003"
	chained.FirstAt = base.FirstAt.Add(6 * time.Second)
	chained.LastAt = chained.FirstAt
	chained.FirstSequence, chained.LastSequence = 3, 3

	aggregator, err := aggregate.New(aggregate.Options{Window: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []workloadtypes.ActivityRecord{chained, atBoundary, base} {
		if err := aggregator.Add(record); err != nil {
			t.Fatal(err)
		}
	}
	records := aggregator.Records()
	if len(records) != 2 || records[0].Count != 2 || records[1].Count != 1 {
		t.Fatalf("anchored window records=%+v", records)
	}
}

func TestAggregatorRejectsConflictingDuplicateAndBoundsPendingInput(t *testing.T) {
	first := aggregateFileRecord(t, "act_duplicate01", "ses_aggregate_a", "exec_fixture0001", "cov_file000001")
	duplicate := first
	conflict := first
	conflict.Count = 2
	second := first
	second.ID = "act_duplicate02"

	aggregator, err := aggregate.New(aggregate.Options{Window: time.Second, MaxInputs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Add(duplicate); err != nil {
		t.Fatalf("idempotent duplicate error=%v", err)
	}
	if err := aggregator.Add(conflict); !errors.Is(err, aggregate.ErrDuplicateID) {
		t.Fatalf("conflicting duplicate error=%v", err)
	}
	if err := aggregator.Add(second); !errors.Is(err, aggregate.ErrCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
}

func TestAggregatorRejectsCrossBoundOwnerAndProcessExecution(t *testing.T) {
	aggregator, err := aggregate.New(aggregate.Options{Window: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ownerMismatch := aggregateFileRecord(
		t,
		"act_ownerbad001",
		"ses_aggregate_a",
		"exec_fixture0001",
		"cov_file000001",
	)
	ownerMismatch.Owner, err = workloadtypes.NewDisposableOwner(
		"ses_different_owner",
		"lima",
		"incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Add(ownerMismatch); !errors.Is(err, aggregate.ErrInvalidRecord) {
		t.Fatalf("cross-bound disposable owner error=%v", err)
	}

	executionMismatch := aggregateProcessRecord(
		t,
		"act_execbad0001",
		time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		1,
	)
	subject := executionMismatch.Subject.(workloadtypes.ProcessSubject)
	subject.ExecutionID = "exec_fixture0002"
	executionMismatch.Subject = subject
	if err := aggregator.Add(executionMismatch); !errors.Is(err, aggregate.ErrInvalidRecord) {
		t.Fatalf("cross-bound process execution error=%v", err)
	}
}

func TestAggregatorRejectsUnboundedOptions(t *testing.T) {
	for _, options := range []aggregate.Options{
		{Window: aggregate.MinimumWindow - 1},
		{Window: aggregate.MaximumWindow + 1},
		{Window: time.Second, MaxInputs: -1},
		{Window: time.Second, MaxInputs: aggregate.MaximumInputs + 1},
	} {
		if _, err := aggregate.New(options); !errors.Is(err, aggregate.ErrInvalidOptions) {
			t.Fatalf("options=%+v error=%v", options, err)
		}
	}
}

func TestAggregatorClonesInputsOutputsAndSaturatesWithDisclosure(t *testing.T) {
	first := aggregateFileRecord(t, "act_overflow001", "ses_aggregate_a", "exec_fixture0001", "cov_file000001")
	first.Count = math.MaxUint64
	first.Bytes = math.MaxUint64
	first.Truncation = []string{"path-truncated"}
	second := first
	second.ID = "act_overflow002"
	second.Count = 1
	second.Bytes = 1
	second.FirstAt = first.FirstAt.Add(time.Millisecond)
	second.LastAt = second.FirstAt
	second.FirstSequence, second.LastSequence = 2, 2

	aggregator, err := aggregate.New(aggregate.Options{Window: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Add(second); err != nil {
		t.Fatal(err)
	}
	subject := first.Subject.(workloadtypes.FileSubject)
	subject.Path = "/workspace/mutated-after-add"
	first.Subject = subject
	first.Truncation[0] = "mutated-after-add"

	records := aggregator.Records()
	if len(records) != 1 {
		t.Fatalf("records=%+v", records)
	}
	record := records[0]
	fileSubject := record.Subject.(workloadtypes.FileSubject)
	if fileSubject.Path != "/workspace/result.txt" ||
		record.Count != math.MaxUint64 ||
		record.Bytes != math.MaxUint64 ||
		!slices.Contains(record.Truncation, "count-overflow") ||
		!slices.Contains(record.Truncation, "bytes-overflow") {
		t.Fatalf("overflow/clone record=%+v", record)
	}
	fileSubject.Path = "/workspace/mutated-output"
	records[0].Subject = fileSubject
	records[0].Truncation[0] = "mutated-output"
	again := aggregator.Records()[0]
	if again.Subject.(workloadtypes.FileSubject).Path != "/workspace/result.txt" ||
		slices.Contains(again.Truncation, "mutated-output") {
		t.Fatalf("returned record aliases aggregator state: %+v", again)
	}
}

func TestAggregatorConcurrentAddIsDeterministic(t *testing.T) {
	aggregator, err := aggregate.New(aggregate.Options{
		Window:    time.Minute,
		MaxInputs: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		record := aggregateFileRecord(
			t,
			fmt.Sprintf("act_concurrent_%03d", index),
			"ses_aggregate_a",
			"exec_fixture0001",
			"cov_file000001",
		)
		record.FirstAt = record.FirstAt.Add(time.Duration(index) * time.Millisecond)
		record.LastAt = record.FirstAt
		record.FirstSequence = uint64(index + 1)
		record.LastSequence = record.FirstSequence
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if addErr := aggregator.Add(record); addErr != nil {
				t.Errorf("Add: %v", addErr)
			}
		}()
	}
	close(start)
	wait.Wait()
	records := aggregator.Records()
	if len(records) != 1 || records[0].Count != 100 ||
		records[0].FirstSequence != 1 || records[0].LastSequence != 100 {
		t.Fatalf("concurrent aggregate=%+v", records)
	}
}

func aggregateFileRecord(
	t *testing.T,
	id, sessionID, executionID, coverageID string,
) workloadtypes.ActivityRecord {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: sessionID,
		Actor: &workloadtypes.Actor{ExecutionID: executionID, PID: 42, UID: 1000, GID: 1000},
		Kind:  workloadtypes.ActivityFile, Operation: "write",
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: "/workspace/result.txt",
			PathState: "resolved", PathClass: "workspace", FileType: "regular",
			Device: 8, Inode: 99,
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, Bytes: 10, FirstAt: at, LastAt: at,
		FirstSequence: 1, LastSequence: 1,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  coverageID, RedactionStatus: workloadtypes.RedactionPending,
	}
}

func aggregateNetworkRecord(
	t *testing.T,
	id string,
	at time.Time,
	sequence, socketCookie uint64,
) workloadtypes.ActivityRecord {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id,
		Owner: owner, SessionID: "ses_aggregate_a",
		Actor: &workloadtypes.Actor{
			ExecutionID: "exec_fixture0001", PID: 42, UID: 1000, GID: 1000,
		},
		Kind: workloadtypes.ActivityConnection, Operation: "connect",
		Subject: workloadtypes.NetworkSubject{
			Kind:     workloadtypes.ActivityConnection,
			Protocol: "tcp", IP: "1.1.1.1", Port: 443,
			DomainAttribution: workloadtypes.AttributionUnknown,
			CorrelationReason: "literal-or-uncorrelated-ip",
			Route:             "direct", Direction: "egress", SocketCookie: socketCookie,
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, Bytes: 10, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution: workloadtypes.AttributionExact,
		CoverageID:  "cov_network0001", RedactionStatus: workloadtypes.RedactionPending,
	}
}

func aggregateDNSRecord(
	t *testing.T,
	id string,
	at time.Time,
	sequence uint64,
) workloadtypes.ActivityRecord {
	t.Helper()
	record := aggregateNetworkRecord(t, id, at, sequence, 0)
	record.Kind = workloadtypes.ActivityDNS
	record.Operation = "resolve"
	record.Bytes = 0
	record.Subject = workloadtypes.DNSSubject{
		Kind: workloadtypes.ActivityDNS, Query: "example.com", QueryType: "A",
		Answers: []string{"93.184.216.34"}, TTLSeconds: 60,
		ResponseCode: "NOERROR", Resolver: "1.1.1.1",
	}
	record.CoverageID = "cov_dnsrecord01"
	return record
}

func aggregateProcessRecord(
	t *testing.T,
	id string,
	at time.Time,
	sequence uint64,
) workloadtypes.ActivityRecord {
	t.Helper()
	record := aggregateNetworkRecord(t, id, at, sequence, 0)
	record.Kind = workloadtypes.ActivityProcess
	record.Operation = "exec"
	record.Bytes = 0
	record.Subject = workloadtypes.ProcessSubject{
		Kind: workloadtypes.ActivityProcess, ExecutionID: "exec_fixture0001",
		Executable: "/usr/bin/git", Argv: []string{"git", "status"},
		Cwd: "/workspace", GuestIdentity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "developer", Group: "developer",
		},
	}
	record.CoverageID = "cov_process0001"
	return record
}

func cloneActor(value *workloadtypes.Actor) *workloadtypes.Actor {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
