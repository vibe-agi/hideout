------------------------ MODULE DisposableRecovery ------------------------
EXTENDS Naturals

CONSTANTS MaxFalse

VARIABLES shape,
          recordInitiallyExists,
          recordExists,
          recordDisposable,
          identityMatches,
          ownerProof,
          journalExists,
          intent,
          durableIntentValid,
          intentEverPersisted,
          backendState,
          absenceStreak,
          absenceProved,
          lastSampleFalse,
          falseBudget,
          cleanupCalls,
          journalRemovalProved,
          outcome

vars == <<shape, recordInitiallyExists, recordExists, recordDisposable,
          identityMatches, ownerProof, journalExists, intent,
          durableIntentValid, intentEverPersisted, backendState,
          absenceStreak, absenceProved, lastSampleFalse, falseBudget,
          cleanupCalls, journalRemovalProved, outcome>>

Shapes == {"record-only", "record-intent", "intent-only",
           "legacy-journal-only"}
OwnerProofs == {"idle", "live", "unknown"}
IntentStates == {"none", "planned", "backend-absent",
                 "metadata-cleaning", "blocked"}
BackendStates == {"present", "absent"}
Outcomes == {"idle", "active", "journal-removed", "blocked", "removed"}
ForwardIntents == {"planned", "backend-absent", "metadata-cleaning"}

ShapeHasRecord(value) == value \in {"record-only", "record-intent"}
ShapeHasIntent(value) == value \in {"record-intent", "intent-only"}

\* Initial states include every durable crash shape. Advanced intents can only
\* be seeded with an absent backend: they represent proof already checkpointed
\* by an earlier process. Intent validity is independent so strict rejection
\* of corrupted historical residue is explored too.
Init ==
    \E initialShape \in Shapes,
       initialIntent \in ForwardIntents,
       disposable \in BOOLEAN,
       exactIdentity \in BOOLEAN,
       owners \in OwnerProofs,
       validIntent \in BOOLEAN,
       backend \in BackendStates :
        /\ shape = initialShape
        /\ recordInitiallyExists = ShapeHasRecord(initialShape)
        /\ recordExists = recordInitiallyExists
        /\ recordDisposable = disposable
        /\ identityMatches = exactIdentity
        /\ ownerProof = owners
        /\ journalExists =
              (initialShape \in {"record-intent", "intent-only",
                                 "legacy-journal-only"})
        /\ intent =
              IF ShapeHasIntent(initialShape) THEN initialIntent ELSE "none"
        /\ durableIntentValid =
              IF ShapeHasIntent(initialShape) THEN validIntent ELSE FALSE
        /\ intentEverPersisted = ShapeHasIntent(initialShape)
        /\ backendState = backend
        /\ (intent \in {"backend-absent", "metadata-cleaning"} =>
              backendState = "absent" /\ durableIntentValid)
        /\ absenceStreak = 0
        /\ absenceProved =
              (intent \in {"backend-absent", "metadata-cleaning"})
        /\ lastSampleFalse = FALSE
        /\ falseBudget = MaxFalse
        /\ cleanupCalls = 0
        /\ journalRemovalProved = FALSE
        /\ outcome = "idle"

CanAdmitRecord ==
    /\ recordExists
    /\ recordDisposable
    /\ identityMatches
    /\ ownerProof = "idle"
    /\ (intent = "none" \/ durableIntentValid)

CanResumeIntentOnly ==
    /\ ~recordExists
    /\ journalExists
    /\ durableIntentValid
    /\ intent \in {"backend-absent", "metadata-cleaning"}
    /\ backendState = "absent"
    /\ ownerProof = "idle"

\* A trusted record can create the durable intent. This transition is the
\* persist-before-destroy boundary: no backend effect occurs in this action.
AdmitRecordOnly ==
    /\ outcome \in {"idle", "blocked"}
    /\ CanAdmitRecord
    /\ intent = "none"
    /\ journalExists' = TRUE
    /\ intent' = "planned"
    /\ durableIntentValid' = TRUE
    /\ intentEverPersisted' = TRUE
    /\ outcome' = "active"
    /\ absenceStreak' = 0
    /\ lastSampleFalse' = FALSE
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    backendState, absenceProved, falseBudget, cleanupCalls,
                    journalRemovalProved>>

ResumeRecordIntent ==
    /\ outcome \in {"idle", "blocked"}
    /\ CanAdmitRecord
    /\ intent # "none"
    /\ intent' = IF intent = "blocked" THEN "planned" ELSE intent
    /\ outcome' = "active"
    /\ absenceStreak' = 0
    /\ lastSampleFalse' = FALSE
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, durableIntentValid, intentEverPersisted,
                    backendState, absenceProved, falseBudget, cleanupCalls,
                    journalRemovalProved>>

\* Missing-record recovery never enters the backend deletion phase. Only a
\* valid intent that already checkpointed absence may converge metadata.
ResumeIntentOnly ==
    /\ outcome \in {"idle", "blocked"}
    /\ CanResumeIntentOnly
    /\ outcome' = "active"
    /\ absenceStreak' = 0
    /\ lastSampleFalse' = FALSE
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, intent, durableIntentValid,
                    intentEverPersisted, backendState, absenceProved,
                    falseBudget, cleanupCalls, journalRemovalProved>>

\* Every missing or contradictory proof is terminal until external
\* revalidation. Legacy journal-only state remains without an intent.
RefuseUnproved ==
    /\ outcome = "idle"
    /\ ~(CanAdmitRecord \/ CanResumeIntentOnly)
    /\ outcome' = "blocked"
    /\ intent' =
          IF intent # "none" /\ durableIntentValid THEN "blocked" ELSE intent
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, durableIntentValid, intentEverPersisted,
                    backendState, absenceStreak, absenceProved,
                    lastSampleFalse, falseBudget, cleanupCalls,
                    journalRemovalProved>>

\* Manager is the sole backend authority. It requires the exact trusted record
\* on every destructive call; a valid intent-only residue cannot reach here.
DeleteBackend ==
    /\ outcome = "active"
    /\ intent = "planned"
    /\ durableIntentValid
    /\ recordExists
    /\ recordDisposable
    /\ identityMatches
    /\ ownerProof = "idle"
    /\ backendState = "present"
    /\ backendState' = "absent"
    /\ cleanupCalls' = cleanupCalls + 1
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, intent, durableIntentValid,
                    intentEverPersisted, absenceStreak, absenceProved,
                    lastSampleFalse, falseBudget, journalRemovalProved,
                    outcome>>

TrueAbsentSample ==
    /\ outcome = "active"
    /\ intent = "planned"
    /\ backendState = "absent"
    /\ absenceStreak < 2
    /\ absenceStreak' = absenceStreak + 1
    /\ lastSampleFalse' = FALSE
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, intent, durableIntentValid,
                    intentEverPersisted, backendState, absenceProved,
                    falseBudget, cleanupCalls, journalRemovalProved,
                    outcome>>

\* Inventory may transiently lie once, but false absence samples cannot be
\* consecutive. Removing ~lastSampleFalse yields the false-green trace that
\* the two-sample implementation is intended to reject.
FalseAbsentSample ==
    /\ outcome = "active"
    /\ intent = "planned"
    /\ backendState = "present"
    /\ falseBudget > 0
    /\ ~lastSampleFalse
    /\ absenceStreak < 2
    /\ absenceStreak' = absenceStreak + 1
    /\ lastSampleFalse' = TRUE
    /\ falseBudget' = falseBudget - 1
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, intent, durableIntentValid,
                    intentEverPersisted, backendState, absenceProved,
                    cleanupCalls, journalRemovalProved, outcome>>

PresentSample ==
    /\ outcome = "active"
    /\ intent = "planned"
    /\ backendState = "present"
    /\ absenceStreak' = 0
    /\ lastSampleFalse' = FALSE
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, intent, durableIntentValid,
                    intentEverPersisted, backendState, absenceProved,
                    falseBudget, cleanupCalls, journalRemovalProved,
                    outcome>>

CheckpointAbsent ==
    /\ outcome = "active"
    /\ intent = "planned"
    /\ absenceStreak = 2
    /\ intent' = "backend-absent"
    /\ absenceProved' = TRUE
    /\ absenceStreak' = 0
    /\ lastSampleFalse' = FALSE
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, durableIntentValid, intentEverPersisted,
                    backendState, falseBudget, cleanupCalls,
                    journalRemovalProved, outcome>>

BeginMetadata ==
    /\ outcome = "active"
    /\ intent = "backend-absent"
    /\ absenceProved
    /\ intent' = "metadata-cleaning"
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, durableIntentValid, intentEverPersisted,
                    backendState, absenceStreak, absenceProved,
                    lastSampleFalse, falseBudget, cleanupCalls,
                    journalRemovalProved, outcome>>

\* Lifecycle metadata is removed while a Manager-owned record still exists.
\* Valid intent-only residue has no record, so journal removal completes it.
RemoveJournal ==
    /\ outcome = "active"
    /\ intent = "metadata-cleaning"
    /\ absenceProved
    /\ journalExists
    /\ journalExists' = FALSE
    /\ intent' = "none"
    /\ journalRemovalProved' = TRUE
    /\ outcome' = IF recordExists THEN "journal-removed" ELSE "removed"
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    durableIntentValid, intentEverPersisted, backendState,
                    absenceStreak, absenceProved, lastSampleFalse,
                    falseBudget, cleanupCalls>>

RemoveRecordLast ==
    /\ outcome = "journal-removed"
    /\ recordExists
    /\ ~journalExists
    /\ recordExists' = FALSE
    /\ outcome' = "removed"
    /\ UNCHANGED <<shape, recordInitiallyExists, recordDisposable,
                    identityMatches, ownerProof, journalExists, intent,
                    durableIntentValid, intentEverPersisted, backendState,
                    absenceStreak, absenceProved, lastSampleFalse,
                    falseBudget, cleanupCalls, journalRemovalProved>>

BlockActive ==
    /\ outcome = "active"
    /\ intent # "none"
    /\ intent' = "blocked"
    /\ outcome' = "blocked"
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, durableIntentValid, intentEverPersisted,
                    backendState, absenceStreak, absenceProved,
                    lastSampleFalse, falseBudget, cleanupCalls,
                    journalRemovalProved>>

\* A crash loses only local admission and observation streaks. All record,
\* intent, backend, and checkpoint state is durable and replayable.
Crash ==
    /\ outcome \in {"active", "journal-removed"}
    /\ outcome' = "idle"
    /\ absenceStreak' = 0
    /\ lastSampleFalse' = FALSE
    /\ UNCHANGED <<shape, recordInitiallyExists, recordExists,
                    recordDisposable, identityMatches, ownerProof,
                    journalExists, intent, durableIntentValid,
                    intentEverPersisted, backendState, absenceProved,
                    falseBudget, cleanupCalls, journalRemovalProved>>

Quiescent ==
    /\ outcome \in {"blocked", "removed"}
    /\ UNCHANGED vars

Next ==
    \/ AdmitRecordOnly
    \/ ResumeRecordIntent
    \/ ResumeIntentOnly
    \/ RefuseUnproved
    \/ DeleteBackend
    \/ TrueAbsentSample
    \/ FalseAbsentSample
    \/ PresentSample
    \/ CheckpointAbsent
    \/ BeginMetadata
    \/ RemoveJournal
    \/ RemoveRecordLast
    \/ BlockActive
    \/ Crash
    \/ Quiescent

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ shape \in Shapes
    /\ recordInitiallyExists \in BOOLEAN
    /\ recordExists \in BOOLEAN
    /\ recordDisposable \in BOOLEAN
    /\ identityMatches \in BOOLEAN
    /\ ownerProof \in OwnerProofs
    /\ journalExists \in BOOLEAN
    /\ intent \in IntentStates
    /\ durableIntentValid \in BOOLEAN
    /\ intentEverPersisted \in BOOLEAN
    /\ backendState \in BackendStates
    /\ absenceStreak \in 0..2
    /\ absenceProved \in BOOLEAN
    /\ lastSampleFalse \in BOOLEAN
    /\ falseBudget \in 0..MaxFalse
    /\ cleanupCalls \in 0..1
    /\ journalRemovalProved \in BOOLEAN
    /\ outcome \in Outcomes

PersistBeforeDestroy ==
    cleanupCalls > 0 =>
        durableIntentValid /\ intentEverPersisted

DestructionRequiresExactRecordAuthority ==
    cleanupCalls > 0 =>
        recordDisposable /\ identityMatches /\ ownerProof = "idle"

IntentOnlyNeverDeletesBackend ==
    shape = "intent-only" => cleanupCalls = 0

LegacyJournalNeverGainsDestructiveAuthority ==
    shape = "legacy-journal-only" => cleanupCalls = 0

StableAbsenceBeforeMetadata ==
    (intent \in {"backend-absent", "metadata-cleaning"} \/
     journalRemovalProved) =>
        absenceProved /\ backendState = "absent"

RecordLastForRecordOrigin ==
    recordInitiallyExists /\ ~recordExists => ~journalExists

SuccessfulRemovalConverges ==
    outcome = "removed" => ~recordExists /\ ~journalExists

JournalRemovalPrecedesRecordRemoval ==
    recordInitiallyExists /\ ~recordExists => journalRemovalProved

AtMostOneBackendCleanup ==
    cleanupCalls <= 1

=============================================================================
