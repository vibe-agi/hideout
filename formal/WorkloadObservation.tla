-------------------- MODULE WorkloadObservation --------------------
EXTENDS Naturals, FiniteSets

CONSTANTS Owners, Processes, MaxSequence, MaxRecords

ASSUME /\ Owners # {}
       /\ Processes # {}
       /\ MaxSequence \in Nat \ {0}
       /\ MaxRecords \in Nat \ {0}

NoOwner == "no-owner"
LifecycleOwner == CHOOSE owner \in Owners : TRUE
CoverageStates == {"Available", "Partial", "Unavailable"}
MaintenanceStates == {"idle", "requested", "complete"}
TargetStates == {"running", "exited"}
DrainStates == {"idle", "draining", "sealed", "forced"}

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
          targetState,
          relayPending,
          drainState,
          goodbye,
          finalReceipt,
          transportClosed,
          completionReceipt,
          sessionCompleted,
          pruneOwner,
          pruneState,
          cleanupOwner,
          cleanupState

vars == <<processOwner, processHistory, sequence, generation, dropped,
          retentionGap, retentionRecorded, coverage, coverageHistory, records,
          targetState, relayPending, drainState, goodbye, finalReceipt,
          transportClosed,
          completionReceipt, sessionCompleted,
          pruneOwner, pruneState, cleanupOwner, cleanupState>>

relayVars == <<targetState, relayPending, drainState,
               goodbye, finalReceipt, transportClosed, completionReceipt,
               sessionCompleted>>

OwnerLive(owner) ==
    cleanupState # "complete" \/ cleanupOwner # owner

OwnerObserving(owner) ==
    owner # LifecycleOwner \/
        (targetState = "running" /\ ~sessionCompleted)

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
    /\ targetState = "running"
    /\ relayPending = 0
    /\ drainState = "idle"
    /\ goodbye = FALSE
    /\ finalReceipt = FALSE
    /\ transportClosed = FALSE
    /\ completionReceipt = FALSE
    /\ sessionCompleted = FALSE
    /\ pruneOwner = NoOwner
    /\ pruneState = "idle"
    /\ cleanupOwner = NoOwner
    /\ cleanupState = "idle"

Register(process, owner) ==
    /\ ObservationOpen
    /\ process \in Processes
    /\ owner \in Owners
    /\ OwnerObserving(owner)
    /\ processOwner[process] = NoOwner
    /\ processOwner' = [processOwner EXCEPT ![process] = owner]
    /\ processHistory' =
        processHistory \cup {[process |-> process, owner |-> owner]}
    /\ UNCHANGED <<sequence, generation, dropped, retentionGap,
                    retentionRecorded, coverage, coverageHistory, records,
                    pruneOwner, pruneState, cleanupOwner, cleanupState>>
    /\ UNCHANGED relayVars

Unregister(process) ==
    LET owner == processOwner[process] IN
    /\ ObservationOpen
    /\ process \in Processes
    /\ owner \in Owners
    /\ OwnerObserving(owner)
    /\ processOwner' = [processOwner EXCEPT ![process] = NoOwner]
    /\ UNCHANGED <<processHistory, sequence, generation, dropped, retentionGap,
                    retentionRecorded, coverage, coverageHistory, records,
                    pruneOwner, pruneState, cleanupOwner, cleanupState>>
    /\ UNCHANGED relayVars

Emit(process) ==
    LET owner == processOwner[process] IN
    /\ ObservationOpen
    /\ process \in Processes
    /\ owner \in Owners
    /\ OwnerObserving(owner)
    /\ sequence[owner] < MaxSequence
    /\ IF owner = LifecycleOwner
       THEN relayPending < MaxSequence
       ELSE TRUE
    /\ Cardinality(records) < MaxRecords
    /\ sequence' = [sequence EXCEPT ![owner] = @ + 1]
    /\ records' =
        records \cup
            {[owner |-> owner,
              process |-> process,
              sequence |-> sequence[owner] + 1]}
    /\ relayPending' =
        IF owner = LifecycleOwner
        THEN relayPending + 1
        ELSE relayPending
    /\ UNCHANGED <<processOwner, processHistory, generation, dropped,
                    retentionGap, retentionRecorded, coverage,
                    coverageHistory, pruneOwner, pruneState, cleanupOwner,
                    cleanupState>>
    /\ UNCHANGED <<targetState, drainState, goodbye, finalReceipt,
                    transportClosed,
                    completionReceipt, sessionCompleted>>

RecordPartial(owner) ==
    /\ coverage' = [coverage EXCEPT ![owner] = "Partial"]
    /\ coverageHistory' =
        [coverageHistory EXCEPT ![owner] = @ \cup {"Partial"}]

LoseEvent(owner) ==
    /\ ObservationOpen
    /\ owner \in Owners
    /\ OwnerObserving(owner)
    /\ sequence[owner] < MaxSequence
    /\ dropped[owner] < MaxSequence
    /\ sequence' = [sequence EXCEPT ![owner] = @ + 1]
    /\ dropped' = [dropped EXCEPT ![owner] = @ + 1]
    /\ RecordPartial(owner)
    /\ UNCHANGED <<processOwner, processHistory, generation, retentionGap,
                    retentionRecorded, records, pruneOwner, pruneState,
                    cleanupOwner, cleanupState>>
    /\ UNCHANGED relayVars

Overflow(process) ==
    LET owner == processOwner[process] IN
    /\ ObservationOpen
    /\ process \in Processes
    /\ owner \in Owners
    /\ OwnerObserving(owner)
    /\ Cardinality(records) >= MaxRecords
    /\ dropped[owner] < MaxSequence
    /\ dropped' = [dropped EXCEPT ![owner] = @ + 1]
    /\ RecordPartial(owner)
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation,
                    retentionGap, retentionRecorded, records, pruneOwner,
                    pruneState, cleanupOwner, cleanupState>>
    /\ UNCHANGED relayVars

RestartCollector(owner) ==
    /\ ObservationOpen
    /\ owner \in Owners
    /\ OwnerObserving(owner)
    /\ generation[owner] = 1
    /\ generation' = [generation EXCEPT ![owner] = 2]
    /\ RecordPartial(owner)
    /\ UNCHANGED <<processOwner, processHistory, sequence, dropped,
                    retentionGap, retentionRecorded, records, pruneOwner,
                    pruneState, cleanupOwner, cleanupState>>
    /\ UNCHANGED relayVars

RecoverCoverage(owner) ==
    /\ ObservationOpen
    /\ owner \in Owners
    /\ OwnerObserving(owner)
    /\ coverage[owner] = "Partial"
    /\ coverage' = [coverage EXCEPT ![owner] = "Available"]
    /\ retentionGap' = [retentionGap EXCEPT ![owner] = FALSE]
    /\ coverageHistory' =
        [coverageHistory EXCEPT ![owner] = @ \cup {"Available"}]
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionRecorded, records, pruneOwner, pruneState,
                    cleanupOwner, cleanupState>>
    /\ UNCHANGED relayVars

RequestPrune(owner) ==
    /\ pruneState = "idle"
    /\ cleanupState # "complete"
    /\ owner \in Owners
    /\ pruneOwner' = owner
    /\ pruneState' = "requested"
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, cleanupOwner, cleanupState>>
    /\ UNCHANGED relayVars

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
    /\ UNCHANGED relayVars

RequestCleanup(owner) ==
    /\ cleanupState = "idle"
    /\ owner \in Owners
    /\ cleanupOwner' = owner
    /\ cleanupState' = "requested"
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, pruneOwner, pruneState>>
    /\ UNCHANGED relayVars

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
    /\ UNCHANGED relayVars

\* The observer transport is modeled separately from the retained-record set:
\* relayPending is the bounded admitted-minus-persisted tail. PersistRelay
\* acknowledges one durable host receipt. DrainObserver seals the producer only
\* after the admitted tail reaches zero and abstracts validation of the exact
\* final counter heartbeat. A goodbye therefore requires finalReceipt first.
\* CloseTransport models authenticated EOF, so a batch-final goodbye alone
\* cannot become a completion receipt. Absolute receipt history is abstracted
\* because only the unpersisted tail affects drain safety.
PersistRelay ==
    /\ ~sessionCompleted
    /\ relayPending > 0
    /\ relayPending' = relayPending - 1
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, targetState, drainState, goodbye, finalReceipt,
                    transportClosed, completionReceipt, sessionCompleted,
                    pruneOwner, pruneState, cleanupOwner, cleanupState>>

TargetExit ==
    /\ OwnerObserving(LifecycleOwner)
    /\ targetState' = "exited"
    /\ drainState' = "draining"
    /\ processOwner' =
        [process \in Processes |->
            IF processOwner[process] = LifecycleOwner
            THEN NoOwner
            ELSE processOwner[process]]
    /\ UNCHANGED <<processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, relayPending, goodbye, finalReceipt,
                    transportClosed,
                    completionReceipt, sessionCompleted, pruneOwner, pruneState,
                    cleanupOwner, cleanupState>>

DrainObserver ==
    /\ targetState = "exited"
    /\ drainState = "draining"
    /\ relayPending = 0
    /\ drainState' = "sealed"
    /\ finalReceipt' = TRUE
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, targetState, relayPending, goodbye, transportClosed,
                    completionReceipt, sessionCompleted, pruneOwner, pruneState,
                    cleanupOwner, cleanupState>>

ForceObserverClose ==
    LET missing == relayPending
        cleaned ==
            cleanupState = "complete" /\ cleanupOwner = LifecycleOwner
    IN
    /\ targetState = "exited"
    /\ drainState = "draining"
    /\ dropped[LifecycleOwner] + missing <= MaxSequence
    /\ dropped' =
        [dropped EXCEPT ![LifecycleOwner] = @ + missing]
    /\ relayPending' = 0
    /\ drainState' = "forced"
    /\ transportClosed' = TRUE
    /\ coverage' =
        IF cleaned
        THEN coverage
        ELSE [coverage EXCEPT ![LifecycleOwner] = "Partial"]
    /\ coverageHistory' =
        [coverageHistory EXCEPT ![LifecycleOwner] = @ \cup {"Partial"}]
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation,
                    retentionGap, retentionRecorded, records, targetState,
                    goodbye, finalReceipt, completionReceipt,
                    sessionCompleted, pruneOwner, pruneState, cleanupOwner,
                    cleanupState>>

EmitGoodbye ==
    /\ drainState = "sealed"
    /\ finalReceipt
    /\ ~goodbye
    /\ goodbye' = TRUE
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, targetState, relayPending,
                    drainState, finalReceipt, transportClosed, completionReceipt,
                    sessionCompleted, pruneOwner,
                    pruneState, cleanupOwner, cleanupState>>

CloseTransport ==
    /\ drainState = "sealed"
    /\ goodbye
    /\ ~transportClosed
    /\ transportClosed' = TRUE
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, targetState, relayPending, drainState, goodbye,
                    finalReceipt,
                    completionReceipt, sessionCompleted, pruneOwner, pruneState,
                    cleanupOwner, cleanupState>>

PersistCompletionReceipt ==
    /\ ~completionReceipt
    /\ \/ /\ drainState = "sealed"
           /\ finalReceipt
           /\ goodbye
           /\ transportClosed
       \/ drainState = "forced"
    /\ completionReceipt' = TRUE
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionGap, retentionRecorded, coverage, coverageHistory,
                    records, targetState, relayPending,
                    drainState, goodbye, finalReceipt, transportClosed,
                    sessionCompleted, pruneOwner, pruneState, cleanupOwner,
                    cleanupState>>

CompleteSession ==
    /\ targetState = "exited"
    /\ completionReceipt
    /\ ~sessionCompleted
    /\ drainState \in {"sealed", "forced"}
    /\ sessionCompleted' = TRUE
    /\ coverage' =
        [coverage EXCEPT ![LifecycleOwner] = "Unavailable"]
    /\ coverageHistory' =
        [coverageHistory EXCEPT
            ![LifecycleOwner] = @ \cup {"Unavailable"}]
    /\ retentionGap' =
        [retentionGap EXCEPT ![LifecycleOwner] = FALSE]
    /\ UNCHANGED <<processOwner, processHistory, sequence, generation, dropped,
                    retentionRecorded, records, targetState,
                    relayPending, drainState, goodbye, finalReceipt,
                    transportClosed,
                    completionReceipt, pruneOwner, pruneState, cleanupOwner,
                    cleanupState>>

Idle == UNCHANGED vars

Next ==
    \/ \E process \in Processes, owner \in Owners : Register(process, owner)
    \/ \E process \in Processes : Unregister(process)
    \/ \E process \in Processes : Emit(process)
    \/ \E owner \in Owners : LoseEvent(owner)
    \/ \E process \in Processes : Overflow(process)
    \/ \E owner \in Owners : RestartCollector(owner)
    \/ \E owner \in Owners : RecoverCoverage(owner)
    \/ PersistRelay
    \/ TargetExit
    \/ DrainObserver
    \/ ForceObserverClose
    \/ EmitGoodbye
    \/ CloseTransport
    \/ PersistCompletionReceipt
    \/ CompleteSession
    \/ \E owner \in Owners : RequestPrune(owner)
    \/ ApplyPrune
    \/ \E owner \in Owners : RequestCleanup(owner)
    \/ Cleanup
    \/ Idle

MaintenanceFairness ==
    /\ WF_vars(ApplyPrune)
    /\ WF_vars(Cleanup)

RelayFairness ==
    /\ WF_vars(PersistRelay)
    /\ WF_vars(DrainObserver)
    /\ WF_vars(EmitGoodbye)
    /\ WF_vars(CloseTransport)
    /\ WF_vars(PersistCompletionReceipt)
    /\ WF_vars(CompleteSession)

SafetySpec == Init /\ [][Next]_vars

Spec == Init /\ [][Next]_vars /\ MaintenanceFairness /\ RelayFairness

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
    /\ targetState \in TargetStates
    /\ relayPending \in 0..MaxSequence
    /\ drainState \in DrainStates
    /\ goodbye \in BOOLEAN
    /\ finalReceipt \in BOOLEAN
    /\ transportClosed \in BOOLEAN
    /\ completionReceipt \in BOOLEAN
    /\ sessionCompleted \in BOOLEAN
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

RelayReceiptNeverLeadsAdmission ==
    relayPending \in 0..MaxSequence

GracefulDrainIsComplete ==
    /\ drainState = "sealed" =>
          /\ targetState = "exited"
          /\ relayPending = 0
          /\ finalReceipt
    /\ finalReceipt => drainState = "sealed"
    /\ goodbye =>
          /\ drainState = "sealed"
          /\ finalReceipt
    /\ transportClosed =>
          \/ goodbye
          \/ drainState = "forced"

ForcedCloseIsExplicit ==
    drainState = "forced" =>
        /\ targetState = "exited"
        /\ relayPending = 0
        /\ ~finalReceipt
        /\ transportClosed
        /\ "Partial" \in coverageHistory[LifecycleOwner]

SessionCompletionRequiresPersistedTerminalReceipt ==
    sessionCompleted =>
        /\ targetState = "exited"
        /\ completionReceipt
        /\ "Unavailable" \in coverageHistory[LifecycleOwner]
        /\ \A process \in Processes :
              processOwner[process] # LifecycleOwner
        /\ \/ /\ drainState = "sealed"
               /\ finalReceipt
               /\ goodbye
               /\ transportClosed
               /\ relayPending = 0
           \/ /\ drainState = "forced"
               /\ "Partial" \in coverageHistory[LifecycleOwner]

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

TargetExitEventuallyPersistsAndCompletes ==
    targetState = "exited" ~> sessionCompleted

====
