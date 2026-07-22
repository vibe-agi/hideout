------------------------ MODULE AttachReservation ------------------------
EXTENDS FiniteSets

CONSTANTS Runs

VARIABLES runPhase,
          runtimeExists,
          ownerRecord,
          reservedRuns,
          transitionLock,
          reconcilePhase

vars == <<runPhase, runtimeExists, ownerRecord, reservedRuns,
          transitionLock, reconcilePhase>>

NoRun == "none"
Phases == {"idle", "allocated", "reserved", "locked", "runtime", "owner",
           "established", "aborted", "crashed"}
ReconcilePhases == {"idle", "scanning"}

Init ==
    /\ runPhase = [run \in Runs |-> "idle"]
    /\ runtimeExists = [run \in Runs |-> FALSE]
    /\ ownerRecord = [run \in Runs |-> FALSE]
    /\ reservedRuns = {}
    /\ transitionLock = NoRun
    /\ reconcilePhase = "idle"

AllocateSession(run) ==
    /\ runPhase[run] = "idle"
    /\ runPhase' = [runPhase EXCEPT ![run] = "allocated"]
    /\ UNCHANGED <<runtimeExists, ownerRecord, reservedRuns, transitionLock,
                    reconcilePhase>>

\* The reservation waits out any in-flight reconciliation before it is
\* granted, and while held it blocks new reconciliation. The run holds no
\* lock while it waits here, so the rejected lock cycle cannot form.
AcquireReservation(run) ==
    /\ runPhase[run] = "allocated"
    /\ reconcilePhase = "idle"
    /\ reservedRuns' = reservedRuns \cup {run}
    /\ runPhase' = [runPhase EXCEPT ![run] = "reserved"]
    /\ UNCHANGED <<runtimeExists, ownerRecord, transitionLock, reconcilePhase>>

AcquireTransitionLock(run) ==
    /\ runPhase[run] = "reserved"
    /\ transitionLock = NoRun
    /\ transitionLock' = run
    /\ runPhase' = [runPhase EXCEPT ![run] = "locked"]
    /\ UNCHANGED <<runtimeExists, ownerRecord, reservedRuns, reconcilePhase>>

CreateRuntime(run) ==
    /\ runPhase[run] = "locked"
    /\ runtimeExists' = [runtimeExists EXCEPT ![run] = TRUE]
    /\ runPhase' = [runPhase EXCEPT ![run] = "runtime"]
    /\ UNCHANGED <<ownerRecord, reservedRuns, transitionLock, reconcilePhase>>

WriteOwner(run) ==
    /\ runPhase[run] = "runtime"
    /\ ownerRecord' = [ownerRecord EXCEPT ![run] = TRUE]
    /\ runPhase' = [runPhase EXCEPT ![run] = "owner"]
    /\ UNCHANGED <<runtimeExists, reservedRuns, transitionLock, reconcilePhase>>

Promote(run) ==
    /\ runPhase[run] = "owner"
    /\ reservedRuns' = reservedRuns \ {run}
    /\ transitionLock' = NoRun
    /\ runPhase' = [runPhase EXCEPT ![run] = "established"]
    /\ UNCHANGED <<runtimeExists, ownerRecord, reconcilePhase>>

\* Cancellation at any establishment stage removes only the run's own
\* residue and releases only what the run itself holds.
Abort(run) ==
    /\ runPhase[run] \in {"allocated", "reserved", "locked", "runtime", "owner"}
    /\ runtimeExists' = [runtimeExists EXCEPT ![run] = FALSE]
    /\ ownerRecord' = [ownerRecord EXCEPT ![run] = FALSE]
    /\ reservedRuns' = reservedRuns \ {run}
    /\ transitionLock' = IF transitionLock = run THEN NoRun ELSE transitionLock
    /\ runPhase' = [runPhase EXCEPT ![run] = "aborted"]
    /\ UNCHANGED reconcilePhase

\* A daemon crash erases every in-memory fact (reservations, lock, scan) and
\* keeps only durable state (runtime dirs, owner records) as residue.
DaemonCrash ==
    /\ runPhase' = [run \in Runs |->
          IF runPhase[run] \in {"idle", "aborted"} THEN runPhase[run]
          ELSE "crashed"]
    /\ reservedRuns' = {}
    /\ transitionLock' = NoRun
    /\ reconcilePhase' = "idle"
    /\ UNCHANGED <<runtimeExists, ownerRecord>>

ReconcileStart ==
    /\ reconcilePhase = "idle"
    /\ reservedRuns = {}
    /\ reconcilePhase' = "scanning"
    /\ UNCHANGED <<runPhase, runtimeExists, ownerRecord, reservedRuns,
                    transitionLock>>

\* Reconcile judges only observable evidence: durable owner records and a
\* liveness probe of the recorded owner. It never reads the establishing
\* run's private intent, and it never touches the transition lock.
ObservablyOrphan(run) ==
    /\ runtimeExists[run]
    /\ \/ ~ownerRecord[run]
       \/ runPhase[run] = "crashed"

ReconcileScrub(run) ==
    /\ reconcilePhase = "scanning"
    /\ ObservablyOrphan(run)
    /\ runtimeExists' = [runtimeExists EXCEPT ![run] = FALSE]
    /\ ownerRecord' = [ownerRecord EXCEPT ![run] = FALSE]
    /\ UNCHANGED <<runPhase, reservedRuns, transitionLock, reconcilePhase>>

ReconcileFinish ==
    /\ reconcilePhase = "scanning"
    /\ reconcilePhase' = "idle"
    /\ UNCHANGED <<runPhase, runtimeExists, ownerRecord, reservedRuns,
                    transitionLock>>

Next ==
    \/ \E run \in Runs : AllocateSession(run)
    \/ \E run \in Runs : AcquireReservation(run)
    \/ \E run \in Runs : AcquireTransitionLock(run)
    \/ \E run \in Runs : CreateRuntime(run)
    \/ \E run \in Runs : WriteOwner(run)
    \/ \E run \in Runs : Promote(run)
    \/ \E run \in Runs : Abort(run)
    \/ DaemonCrash
    \/ ReconcileStart
    \/ \E run \in Runs : ReconcileScrub(run)
    \/ ReconcileFinish

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ runPhase \in [Runs -> Phases]
    /\ runtimeExists \in [Runs -> BOOLEAN]
    /\ ownerRecord \in [Runs -> BOOLEAN]
    /\ reservedRuns \subseteq Runs
    /\ transitionLock \in Runs \cup {NoRun}
    /\ reconcilePhase \in ReconcilePhases

\* The GROUP 3 #10 property: an establishing run that has created its
\* runtime never observes it scrubbed out from under it.
EstablishingRuntimeIntact ==
    \A run \in Runs :
        runPhase[run] \in {"runtime", "owner"} => runtimeExists[run]

EstablishedIsDurable ==
    \A run \in Runs :
        runPhase[run] = "established" =>
            runtimeExists[run] /\ ownerRecord[run]

ReservationBlocksReconcile ==
    reservedRuns # {} => reconcilePhase = "idle"

\* The ordering contract that avoids the rejected lock cycle: a run still
\* waiting for its reservation never holds the transition lock.
WaitersHoldNoLock ==
    \A run \in Runs :
        runPhase[run] \in {"idle", "allocated"} => transitionLock # run

LockHolderIsEstablishing ==
    transitionLock # NoRun =>
        runPhase[transitionLock] \in {"locked", "runtime", "owner"}

OwnerImpliesRuntime ==
    \A run \in Runs : ownerRecord[run] => runtimeExists[run]

=============================================================================
