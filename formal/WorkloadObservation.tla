-------------------- MODULE WorkloadObservation --------------------
EXTENDS Naturals, FiniteSets

CONSTANTS Owners, Processes, MaxSequence, MaxRecords

ASSUME /\ Owners # {}
       /\ Processes # {}
       /\ MaxSequence \in Nat \ {0}
       /\ MaxRecords \in Nat \ {0}

NoOwner == "no-owner"
CoverageStates == {"Available", "Partial", "Unavailable"}
MaintenanceStates == {"idle", "requested", "complete"}

RecordUniverse ==
    [owner : Owners, process : Processes, sequence : 1..MaxSequence]

ProcessOwnerPairUniverse ==
    [process : Processes, owner : Owners]

VARIABLES processOwner,
          processHistory,
          sequence,
          generation,
          dropped,
          retentionGap,
          retentionRecorded,
          coverage,
          coverageHistory,
          records,
          pruneOwner,
          pruneState,
          cleanupOwner,
          cleanupState

vars == <<processOwner, processHistory, sequence, generation, dropped,
          retentionGap, retentionRecorded, coverage, coverageHistory, records,
          pruneOwner, pruneState, cleanupOwner, cleanupState>>

OwnerLive(owner) ==
    cleanupState # "complete" \/ cleanupOwner # owner

\* A maintenance request represents the store's serialized mutation cut.
\* Observation can build any bounded prefix before that cut. Prune and cleanup
\* may then be requested together and finish in either order, but unrelated
\* appends do not obscure which records either atomic step removed.
ObservationOpen ==
    pruneState = "idle" /\ cleanupState = "idle"

Init ==
    /\ processOwner = [process \in Processes |-> NoOwner]
    /\ processHistory = {}
    /\ sequence = [owner \in Owners |-> 0]
    /\ generation = [owner \in Owners |-> 1]
    /\ dropped = [owner \in Owners |-> 0]
    /\ retentionGap = [owner \in Owners |-> FALSE]
    /\ retentionRecorded = [owner \in Owners |-> FALSE]
    /\ coverage = [owner \in Owners |-> "Available"]
    /\ coverageHistory = [owner \in Owners |-> {"Available"}]
    /\ records = {}
    /\ pruneOwner = NoOwner
    /\ pruneState = "idle"
    /\ cleanupOwner = NoOwner
    /\ cleanupState = "idle"

Register(process, owner) ==
    /\ ObservationOpen
    /\ process \in Processes
    /\ owner \in Owners
    /\ processOwner[process] = NoOwner
    /\ processOwner' = [processOwner EXCEPT ![process] = owner]
    /\ processHistory' =
        processHistory \cup {[process |-> process, owner |-> owner]}
    /\ UNCHANGED <<sequence, generation, dropped, retentionGap,
                    retentionRecorded, coverage, coverageHistory, records,
                    pruneOwner, pruneState, cleanupOwner, cleanupState>>

Unregister(process) ==
    /\ ObservationOpen
    /\ process \in Processes
    /\ processOwner[process] # NoOwner
    /\ processOwner' = [processOwner EXCEPT ![process] = NoOwner]
    /\ UNCHANGED <<processHistory, sequence, generation, dropped, retentionGap,
                    retentionRecorded, coverage, coverageHistory, records,
                    pruneOwner, pruneState, cleanupOwner, cleanupState>>

Emit(process) ==
    LET owner == processOwner[process] IN
    /\ ObservationOpen
    /\ process \in Processes
    /\ owner \in Owners
    /\ sequence[owner] < MaxSequence
    /\ Cardinality(records) < MaxRecords
    /\ sequence' = [sequence EXCEPT ![owner] = @ + 1]
    /\ records' =
        records \cup
            {[owner |-> owner,
              process |-> process,
              sequence |-> sequence[owner] + 1]}
    /\ UNCHANGED <<processOwner, processHistory, generation, dropped,
                    retentionGap, retentionRecorded, coverage,
                    coverageHistory, pruneOwner, pruneState, cleanupOwner,
                    cleanupState>>

RecordPartial(owner) ==
    /\ coverage' = [coverage EXCEPT ![owner] = "Partial"]
    /\ coverageHistory' =
        [coverageHistory EXCEPT ![owner] = @ \cup {"Partial"}]

LoseEvent(owner) ==
    /\ ObservationOpen
    /\ owner \in Owners
    /\ sequence[owner] < MaxSequence
    /\ dropped[owner] < MaxSequence
    /\ sequence' = [sequence EXCEPT ![owner] = @ + 1]
    /\ dropped' = [dropped EXCEPT ![owner] = @ + 1]
    /\ RecordPartial(owner)
    /\ UNCHANGED <<processOwner, processHistory, generation, retentionGap,
                    retentionRecorded, records, pruneOwner, pruneState,
                    cleanupOwner, cleanupState>>

Overflow(process) ==
    LET owner == processOwner[process] IN
    /\ ObservationOpen
    /\ process \in Processes
    /\ owner \in Owners
    /\ Cardinality(records) >= MaxRecords
    /\ dropped[owner] < MaxSequence
    /\ dropped' = [dropped EXCEPT ![owner] = @ + 1]
    /\ RecordPartial(owner)
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation,
                    retentionGap, retentionRecorded, records, pruneOwner,
                    pruneState, cleanupOwner, cleanupState>>

RestartCollector(owner) ==
    /\ ObservationOpen
    /\ owner \in Owners
    /\ generation[owner] = 1
    /\ generation' = [generation EXCEPT ![owner] = 2]
    /\ RecordPartial(owner)
    /\ UNCHANGED <<processOwner, processHistory, sequence, dropped,
                    retentionGap, retentionRecorded, records, pruneOwner,
                    pruneState, cleanupOwner, cleanupState>>

RecoverCoverage(owner) ==
    /\ ObservationOpen
    /\ owner \in Owners
    /\ coverage[owner] = "Partial"
    /\ coverage' = [coverage EXCEPT ![owner] = "Available"]
    /\ retentionGap' = [retentionGap EXCEPT ![owner] = FALSE]
    /\ coverageHistory' =
        [coverageHistory EXCEPT ![owner] = @ \cup {"Available"}]
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionRecorded, records, pruneOwner, pruneState,
                    cleanupOwner, cleanupState>>

RequestPrune(owner) ==
    /\ pruneState = "idle"
    /\ cleanupState # "complete"
    /\ owner \in Owners
    /\ pruneOwner' = owner
    /\ pruneState' = "requested"
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, cleanupOwner, cleanupState>>

ApplyPrune ==
    LET cleaned ==
            cleanupState = "complete" /\ cleanupOwner = pruneOwner
    IN
    /\ pruneState = "requested"
    /\ records' =
        {record \in records : record.owner # pruneOwner}
    /\ retentionGap' =
        IF cleaned
        THEN retentionGap
        ELSE [retentionGap EXCEPT ![pruneOwner] = TRUE]
    /\ retentionRecorded' =
        IF cleaned
        THEN retentionRecorded
        ELSE [retentionRecorded EXCEPT ![pruneOwner] = TRUE]
    /\ coverage' =
        IF cleaned
        THEN coverage
        ELSE [coverage EXCEPT ![pruneOwner] = "Partial"]
    /\ coverageHistory' =
        IF cleaned
        THEN coverageHistory
        ELSE [coverageHistory EXCEPT ![pruneOwner] = @ \cup {"Partial"}]
    /\ pruneState' = "complete"
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    pruneOwner, cleanupOwner, cleanupState>>

RequestCleanup(owner) ==
    /\ cleanupState = "idle"
    /\ owner \in Owners
    /\ cleanupOwner' = owner
    /\ cleanupState' = "requested"
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, pruneOwner, pruneState>>

Cleanup ==
    /\ cleanupState = "requested"
    /\ records' =
        {record \in records : record.owner # cleanupOwner}
    /\ processOwner' =
        [process \in Processes |->
            IF processOwner[process] = cleanupOwner
            THEN NoOwner
            ELSE processOwner[process]]
    /\ coverage' = [coverage EXCEPT ![cleanupOwner] = "Unavailable"]
    /\ coverageHistory' =
        [coverageHistory EXCEPT ![cleanupOwner] = @ \cup {"Unavailable"}]
    /\ retentionGap' = [retentionGap EXCEPT ![cleanupOwner] = FALSE]
    /\ cleanupState' = "complete"
    /\ UNCHANGED <<processHistory, sequence, generation, dropped,
                    retentionRecorded, pruneOwner, pruneState, cleanupOwner>>

Idle == UNCHANGED vars

Next ==
    \/ \E process \in Processes, owner \in Owners : Register(process, owner)
    \/ \E process \in Processes : Unregister(process)
    \/ \E process \in Processes : Emit(process)
    \/ \E owner \in Owners : LoseEvent(owner)
    \/ \E process \in Processes : Overflow(process)
    \/ \E owner \in Owners : RestartCollector(owner)
    \/ \E owner \in Owners : RecoverCoverage(owner)
    \/ \E owner \in Owners : RequestPrune(owner)
    \/ ApplyPrune
    \/ \E owner \in Owners : RequestCleanup(owner)
    \/ Cleanup
    \/ Idle

MaintenanceFairness ==
    /\ WF_vars(ApplyPrune)
    /\ WF_vars(Cleanup)

Spec == Init /\ [][Next]_vars /\ MaintenanceFairness

TypeOK ==
    /\ processOwner \in [Processes -> Owners \cup {NoOwner}]
    /\ processHistory \subseteq ProcessOwnerPairUniverse
    /\ sequence \in [Owners -> 0..MaxSequence]
    /\ generation \in [Owners -> {1, 2}]
    /\ dropped \in [Owners -> 0..MaxSequence]
    /\ retentionGap \in [Owners -> BOOLEAN]
    /\ retentionRecorded \in [Owners -> BOOLEAN]
    /\ coverage \in [Owners -> CoverageStates]
    /\ coverageHistory \in [Owners -> SUBSET CoverageStates]
    /\ records \subseteq RecordUniverse
    /\ pruneOwner \in Owners \cup {NoOwner}
    /\ pruneState \in MaintenanceStates
    /\ cleanupOwner \in Owners \cup {NoOwner}
    /\ cleanupState \in MaintenanceStates

OwnerIsolation ==
    \A record \in records :
        [process |-> record.process, owner |-> record.owner]
            \in processHistory

NoFalseAvailableCoverage ==
    \A owner \in Owners :
        coverage[owner] = "Available" =>
            /\ ~retentionGap[owner]
            /\ OwnerLive(owner)

KnownLossIsExplicit ==
    \A owner \in Owners :
        /\ dropped[owner] > 0 =>
              "Partial" \in coverageHistory[owner]
        /\ generation[owner] > 1 =>
              "Partial" \in coverageHistory[owner]

RetentionGapIsExplicit ==
    \A owner \in Owners :
        /\ retentionGap[owner] => coverage[owner] = "Partial"
        /\ retentionRecorded[owner] =>
              "Partial" \in coverageHistory[owner]

\* These are transition safety properties rather than after-the-fact state
\* summaries: they compare the exact pre/post store contents atomically.
ExactOwnerPrune ==
    [][ApplyPrune =>
          records' =
              {record \in records : record.owner # pruneOwner}]_vars

ExactOwnerCleanup ==
    [][Cleanup =>
          records' =
              {record \in records : record.owner # cleanupOwner}]_vars

CleanupCompletionRequiresAbsence ==
    cleanupState = "complete" =>
        /\ coverage[cleanupOwner] = "Unavailable"
        /\ \A record \in records : record.owner # cleanupOwner
        /\ \A process \in Processes :
              processOwner[process] # cleanupOwner

RetentionEventuallyCompletes ==
    pruneState = "requested" ~> pruneState = "complete"

CleanupEventuallyCompletes ==
    cleanupState = "requested" ~> cleanupState = "complete"

====
