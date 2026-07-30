-------------------- MODULE OperatorConfiguration --------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Clients, Profiles, Values, OperationIDs, MaxRevision, MaxRetries

ASSUME /\ Clients # {}
       /\ Profiles # {}
       /\ Values # {}
       /\ OperationIDs # {}
       /\ MaxRevision \in Nat \ {0}
       /\ MaxRetries \in Nat \ {0}

NoClient == "no-client"
NoProfile == "no-profile"
NoValue == "no-value"
NoOperation == "no-operation"

PlanStates ==
    {"none", "planned", "claimed", "applying", "effected",
     "rolling-back", "stale", "completed", "failed"}
OperationStates ==
    {"none", "planned", "claimed", "applying", "effected",
     "rolling-back", "stale", "succeeded", "failed"}
ActiveOperationStates ==
    {"planned", "claimed", "applying", "effected", "rolling-back"}
OwnedOperationStates ==
    {"claimed", "applying", "effected", "rolling-back"}
TerminalOperationStates == {"stale", "succeeded", "failed"}
ResponseStates == {"none", "pending", "lost", "delivered"}

CommitUniverse ==
    [operation : OperationIDs,
     client : Clients,
     profile : Profiles,
     value : Values,
     baseRevision : 0..MaxRevision,
     committedRevision : 0..MaxRevision]

Symmetry ==
    Permutations(Clients) \cup
    Permutations(OperationIDs) \cup
    Permutations(Values)

VARIABLES revision,
          desired,
          plans,
          operations,
          effectCount,
          rollbackCount,
          commits,
          claims,
          disconnected,
          mismatchRejected,
          daemonUp,
          crashUsed

vars ==
    <<revision, desired, plans, operations, effectCount, rollbackCount,
      commits, claims, disconnected, mismatchRejected, daemonUp, crashUsed>>

EmptyPlan ==
    [state |-> "none",
     profile |-> NoProfile,
     value |-> NoValue,
     operation |-> NoOperation,
     baseRevision |-> 0]

EmptyOperation ==
    [state |-> "none",
     client |-> NoClient,
     profile |-> NoProfile,
     value |-> NoValue,
     baseRevision |-> 0,
     response |-> "none",
     retries |-> 0]

Init ==
    /\ revision = [profile \in Profiles |-> 0]
    /\ desired \in [Profiles -> Values]
    /\ plans = [client \in Clients |-> EmptyPlan]
    /\ operations = [operation \in OperationIDs |-> EmptyOperation]
    /\ effectCount = [operation \in OperationIDs |-> 0]
    /\ rollbackCount = [operation \in OperationIDs |-> 0]
    /\ commits = {}
    /\ claims = {}
    /\ disconnected = {}
    /\ mismatchRejected = {}
    /\ daemonUp = TRUE
    /\ crashUsed = FALSE

ProfileHasDurableOwner(profile) ==
    \E operation \in OperationIDs :
        /\ operations[operation].profile = profile
        /\ operations[operation].state \in OwnedOperationStates

ProfileClaimedByOther(profile, operation) ==
    \E other \in claims \ {operation} :
        operations[other].profile = profile

CreatePlan(client, profile, value, operation) ==
    /\ client \in Clients \ disconnected
    /\ profile \in Profiles
    /\ value \in Values
    /\ operation \in OperationIDs
    /\ plans[client].state \in {"none", "stale", "completed", "failed"}
    /\ operations[operation].state = "none"
    /\ revision[profile] < MaxRevision
    /\ plans' =
        [plans EXCEPT
            ![client] =
                [state |-> "planned",
                 profile |-> profile,
                 value |-> value,
                 operation |-> operation,
                 baseRevision |-> revision[profile]]]
    /\ operations' =
        [operations EXCEPT
            ![operation] =
                [state |-> "planned",
                 client |-> client,
                 profile |-> profile,
                 value |-> value,
                 baseRevision |-> revision[profile],
                 response |-> "none",
                 retries |-> 0]]
    /\ UNCHANGED <<revision, desired, effectCount, rollbackCount, commits,
                    claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

\* External writers can invalidate an unclaimed plan. Once an operation owns
\* the profile, serialization is durable across daemon crashes.
ExternalEdit(profile, value) ==
    /\ profile \in Profiles
    /\ value \in Values
    /\ value # desired[profile]
    /\ revision[profile] < MaxRevision
    /\ ~ProfileHasDurableOwner(profile)
    /\ desired' = [desired EXCEPT ![profile] = value]
    /\ revision' = [revision EXCEPT ![profile] = @ + 1]
    /\ UNCHANGED <<plans, operations, effectCount, rollbackCount, commits,
                    claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

Claim(client) ==
    LET operation == plans[client].operation
        profile == plans[client].profile
    IN
    /\ daemonUp
    /\ plans[client].state = "planned"
    /\ plans[client].baseRevision = revision[profile]
    /\ operations[operation].state = "planned"
    /\ ~ProfileHasDurableOwner(profile)
    /\ plans' = [plans EXCEPT ![client].state = "claimed"]
    /\ operations' = [operations EXCEPT ![operation].state = "claimed"]
    /\ claims' = claims \cup {operation}
    /\ UNCHANGED <<revision, desired, effectCount, rollbackCount, commits,
                    disconnected, mismatchRejected, daemonUp, crashUsed>>

\* A crash drops the process-local claim but not the durable phase. Startup
\* recovery must reclaim the exact bound operation before it can continue.
Reclaim(operation) ==
    LET profile == operations[operation].profile IN
    /\ daemonUp
    /\ operation \in OperationIDs \ claims
    /\ operations[operation].state \in OwnedOperationStates
    /\ ~ProfileClaimedByOther(profile, operation)
    /\ claims' = claims \cup {operation}
    /\ UNCHANGED <<revision, desired, plans, operations, effectCount,
                    rollbackCount, commits, disconnected, mismatchRejected,
                    daemonUp, crashUsed>>

BeginApply(client) ==
    LET operation == plans[client].operation IN
    /\ daemonUp
    /\ operation \in claims
    /\ plans[client].state = "claimed"
    /\ operations[operation].state = "claimed"
    /\ plans' = [plans EXCEPT ![client].state = "applying"]
    /\ operations' = [operations EXCEPT ![operation].state = "applying"]
    /\ UNCHANGED <<revision, desired, effectCount, rollbackCount, commits,
                    claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

\* Provider effect and durable success publication are separate crash
\* boundaries. The effect is counted before any terminal outcome exists.
ApplyEffect(client) ==
    LET operation == plans[client].operation IN
    /\ daemonUp
    /\ operation \in claims
    /\ plans[client].state = "applying"
    /\ operations[operation].state = "applying"
    /\ effectCount[operation] = 0
    /\ plans' = [plans EXCEPT ![client].state = "effected"]
    /\ operations' = [operations EXCEPT ![operation].state = "effected"]
    /\ effectCount' = [effectCount EXCEPT ![operation] = 1]
    /\ UNCHANGED <<revision, desired, rollbackCount, commits, claims,
                    disconnected, mismatchRejected, daemonUp, crashUsed>>

BeginRollbackBeforeEffect(client) ==
    LET operation == plans[client].operation IN
    /\ daemonUp
    /\ operation \in claims
    /\ plans[client].state = "applying"
    /\ operations[operation].state = "applying"
    /\ plans' = [plans EXCEPT ![client].state = "rolling-back"]
    /\ operations' =
        [operations EXCEPT ![operation].state = "rolling-back"]
    /\ UNCHANGED <<revision, desired, effectCount, rollbackCount, commits,
                    claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

BeginRollbackAfterEffect(client) ==
    LET operation == plans[client].operation IN
    /\ daemonUp
    /\ operation \in claims
    /\ plans[client].state = "effected"
    /\ operations[operation].state = "effected"
    /\ effectCount[operation] = 1
    /\ plans' = [plans EXCEPT ![client].state = "rolling-back"]
    /\ operations' =
        [operations EXCEPT ![operation].state = "rolling-back"]
    /\ UNCHANGED <<revision, desired, effectCount, rollbackCount, commits,
                    claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

Complete(client) ==
    LET operation == plans[client].operation
        profile == plans[client].profile
    IN
    /\ daemonUp
    /\ operation \in claims
    /\ plans[client].state = "effected"
    /\ operations[operation].state = "effected"
    /\ effectCount[operation] = 1
    /\ plans[client].baseRevision = revision[profile]
    /\ revision[profile] < MaxRevision
    /\ desired' = [desired EXCEPT ![profile] = plans[client].value]
    /\ revision' = [revision EXCEPT ![profile] = @ + 1]
    /\ plans' = [plans EXCEPT ![client].state = "completed"]
    /\ operations' =
        [operations EXCEPT
            ![operation].state = "succeeded",
            ![operation].response = "pending"]
    /\ commits' =
        commits \cup
            {[operation |-> operation,
              client |-> client,
              profile |-> profile,
              value |-> plans[client].value,
              baseRevision |-> plans[client].baseRevision,
              committedRevision |-> revision[profile] + 1]}
    /\ claims' = claims \ {operation}
    /\ UNCHANGED <<effectCount, rollbackCount, disconnected,
                    mismatchRejected, daemonUp, crashUsed>>

Rollback(client) ==
    LET operation == plans[client].operation IN
    /\ daemonUp
    /\ operation \in claims
    /\ plans[client].state = "rolling-back"
    /\ operations[operation].state = "rolling-back"
    /\ rollbackCount[operation] = 0
    /\ plans' = [plans EXCEPT ![client].state = "failed"]
    /\ operations' =
        [operations EXCEPT
            ![operation].state = "failed",
            ![operation].response = "pending"]
    /\ rollbackCount' = [rollbackCount EXCEPT ![operation] = 1]
    /\ claims' = claims \ {operation}
    /\ UNCHANGED <<revision, desired, effectCount, commits, disconnected,
                    mismatchRejected, daemonUp, crashUsed>>

RejectStale(client) ==
    LET operation == plans[client].operation
        profile == plans[client].profile
    IN
    /\ daemonUp
    /\ plans[client].state = "planned"
    /\ plans[client].baseRevision # revision[profile]
    /\ operations[operation].state = "planned"
    /\ plans' = [plans EXCEPT ![client].state = "stale"]
    /\ operations' =
        [operations EXCEPT
            ![operation].state = "stale",
            ![operation].response = "pending"]
    /\ claims' = claims \ {operation}
    /\ UNCHANGED <<revision, desired, effectCount, rollbackCount, commits,
                    disconnected, mismatchRejected, daemonUp, crashUsed>>

DeliverResponse(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].state \in TerminalOperationStates
    /\ operations[operation].response = "pending"
    /\ operations' = [operations EXCEPT ![operation].response = "delivered"]
    /\ UNCHANGED <<revision, desired, plans, effectCount, rollbackCount,
                    commits, claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

LoseResponse(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].state \in TerminalOperationStates
    /\ operations[operation].response = "pending"
    /\ operations' = [operations EXCEPT ![operation].response = "lost"]
    /\ UNCHANGED <<revision, desired, plans, effectCount, rollbackCount,
                    commits, claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

\* An exact retry of accepted work changes only retry metadata. In particular,
\* it cannot invoke the provider effect again.
RetryActive(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].state \in ActiveOperationStates
    /\ operations[operation].retries = 0
    /\ operations' =
        [operations EXCEPT ![operation].retries = 1]
    /\ UNCHANGED <<revision, desired, plans, effectCount, rollbackCount,
                    commits, claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

RetryExact(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].state \in TerminalOperationStates
    /\ operations[operation].response = "lost"
    /\ operations' =
        [operations EXCEPT
            ![operation].response = "delivered",
            ![operation].retries =
                IF @ = 0 THEN 1 ELSE @]
    /\ UNCHANGED <<revision, desired, plans, effectCount, rollbackCount,
                    commits, claims, disconnected, mismatchRejected, daemonUp,
                    crashUsed>>

RetryMismatch(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].state # "none"
    /\ operation \notin mismatchRejected
    /\ mismatchRejected' = mismatchRejected \cup {operation}
    /\ UNCHANGED <<revision, desired, plans, operations, effectCount,
                    rollbackCount, commits, claims, disconnected, daemonUp,
                    crashUsed>>

Disconnect(client) ==
    /\ client \in Clients \ disconnected
    /\ disconnected' = disconnected \cup {client}
    /\ UNCHANGED <<revision, desired, plans, operations, effectCount,
                    rollbackCount, commits, claims, mismatchRejected, daemonUp,
                    crashUsed>>

\* One bounded crash is sufficient to enumerate every phase boundary while
\* still making eventual recovery a valid weak-fair property.
Crash ==
    /\ daemonUp
    /\ ~crashUsed
    /\ daemonUp' = FALSE
    /\ crashUsed' = TRUE
    /\ claims' = {}
    /\ UNCHANGED <<revision, desired, plans, operations, effectCount,
                    rollbackCount, commits, disconnected, mismatchRejected>>

Restart ==
    /\ ~daemonUp
    /\ daemonUp' = TRUE
    /\ UNCHANGED <<revision, desired, plans, operations, effectCount,
                    rollbackCount, commits, claims, disconnected,
                    mismatchRejected, crashUsed>>

Idle == UNCHANGED vars

ResolvePlan(client) == Claim(client) \/ RejectStale(client)

ResolveApplying(client) ==
    ApplyEffect(client) \/ BeginRollbackBeforeEffect(client)

ResolveEffected(client) ==
    Complete(client) \/ BeginRollbackAfterEffect(client)

Next ==
    \/ \E client \in Clients, profile \in Profiles, value \in Values,
          operation \in OperationIDs :
          CreatePlan(client, profile, value, operation)
    \/ \E profile \in Profiles, value \in Values : ExternalEdit(profile, value)
    \/ \E client \in Clients : Claim(client)
    \/ \E operation \in OperationIDs : Reclaim(operation)
    \/ \E client \in Clients : BeginApply(client)
    \/ \E client \in Clients : ApplyEffect(client)
    \/ \E client \in Clients : BeginRollbackBeforeEffect(client)
    \/ \E client \in Clients : BeginRollbackAfterEffect(client)
    \/ \E client \in Clients : Complete(client)
    \/ \E client \in Clients : Rollback(client)
    \/ \E client \in Clients : RejectStale(client)
    \/ \E operation \in OperationIDs : DeliverResponse(operation)
    \/ \E operation \in OperationIDs : LoseResponse(operation)
    \/ \E operation \in OperationIDs : RetryActive(operation)
    \/ \E operation \in OperationIDs : RetryExact(operation)
    \/ \E operation \in OperationIDs : RetryMismatch(operation)
    \/ \E client \in Clients : Disconnect(client)
    \/ Crash
    \/ Restart
    \/ Idle

RecoveryStep ==
    \/ Restart
    \/ \E client \in Clients : ResolvePlan(client)
    \/ \E operation \in OperationIDs : Reclaim(operation)
    \/ \E client \in Clients : BeginApply(client)
    \/ \E client \in Clients : ResolveApplying(client)
    \/ \E client \in Clients : ResolveEffected(client)
    \/ \E client \in Clients : Rollback(client)

ResponseStep ==
    \/ \E operation \in OperationIDs : DeliverResponse(operation)
    \/ \E operation \in OperationIDs : RetryExact(operation)

\* Accepted operations, retries, revisions, crashes, and responses are all
\* bounded and monotonic in this model. Weak fairness of the aggregate daemon
\* step therefore cannot starve one operation behind an infinite stream of
\* work for another operation.
RecoveryFairness == WF_vars(RecoveryStep)

ResponseFairness == WF_vars(ResponseStep)

SafetySpec == Init /\ [][Next]_vars

Spec == Init /\ [][Next]_vars /\ RecoveryFairness /\ ResponseFairness

TypeOK ==
    /\ revision \in [Profiles -> 0..MaxRevision]
    /\ desired \in [Profiles -> Values]
    /\ plans \in
        [Clients ->
            [state : PlanStates,
             profile : Profiles \cup {NoProfile},
             value : Values \cup {NoValue},
             operation : OperationIDs \cup {NoOperation},
             baseRevision : 0..MaxRevision]]
    /\ operations \in
        [OperationIDs ->
            [state : OperationStates,
             client : Clients \cup {NoClient},
             profile : Profiles \cup {NoProfile},
             value : Values \cup {NoValue},
             baseRevision : 0..MaxRevision,
             response : ResponseStates,
             retries : 0..MaxRetries]]
    /\ effectCount \in [OperationIDs -> 0..1]
    /\ rollbackCount \in [OperationIDs -> 0..1]
    /\ commits \subseteq CommitUniverse
    /\ claims \subseteq OperationIDs
    /\ disconnected \subseteq Clients
    /\ mismatchRejected \subseteq OperationIDs
    /\ daemonUp \in BOOLEAN
    /\ crashUsed \in BOOLEAN

StalePlanNeverCommits ==
    \A commit \in commits :
        commit.committedRevision = commit.baseRevision + 1

CommittedOperationMatchesBinding ==
    \A commit \in commits :
        /\ operations[commit.operation].state = "succeeded"
        /\ operations[commit.operation].client = commit.client
        /\ operations[commit.operation].profile = commit.profile
        /\ operations[commit.operation].value = commit.value
        /\ operations[commit.operation].baseRevision = commit.baseRevision

OperationBindingUnique ==
    \A left \in commits, right \in commits :
        left.operation = right.operation =>
            /\ left.client = right.client
            /\ left.profile = right.profile
            /\ left.value = right.value
            /\ left.baseRevision = right.baseRevision

AtMostOneEffectAndRollback ==
    \A operation \in OperationIDs :
        /\ effectCount[operation] <= 1
        /\ rollbackCount[operation] <= 1

EffectHasAuthoritativeOutcome ==
    \A operation \in OperationIDs :
        /\ effectCount[operation] = 1 =>
              operations[operation].state \in
                  {"effected", "rolling-back", "succeeded", "failed"}
        /\ operations[operation].state = "succeeded" =>
              /\ effectCount[operation] = 1
              /\ \E commit \in commits : commit.operation = operation

RollbackNeverPublishesSuccess ==
    \A operation \in OperationIDs :
        rollbackCount[operation] = 1 =>
            /\ operations[operation].state = "failed"
            /\ ~(\E commit \in commits : commit.operation = operation)

ExclusiveProfileClaim ==
    \A left \in claims, right \in claims :
        operations[left].profile = operations[right].profile => left = right

ClaimsBelongToLiveDaemon ==
    /\ ~daemonUp => claims = {}
    /\ \A operation \in claims :
          operations[operation].state \in OwnedOperationStates

MismatchNeverChangesBinding ==
    \A operation \in mismatchRejected :
        /\ operations[operation].state # "none"
        /\ operations[operation].client \in Clients
        /\ operations[operation].profile \in Profiles
        /\ operations[operation].value \in Values

PlanEventuallyTerminal ==
    \A operation \in OperationIDs :
        (operations[operation].state \in ActiveOperationStates)
            ~> (operations[operation].state \in TerminalOperationStates)

EveryClaimEventuallyReleased ==
    \A operation \in OperationIDs :
        (operation \in claims) ~> (operation \notin claims)

CrashEventuallyRestarts ==
    (~daemonUp) ~> daemonUp

RollbackEventuallyTerminal ==
    \A operation \in OperationIDs :
        (operations[operation].state = "rolling-back")
            ~> (operations[operation].state = "failed")

TerminalResponseEventuallyDelivered ==
    \A operation \in OperationIDs :
        /\ (operations[operation].state \in TerminalOperationStates)
        /\ (operations[operation].response \in {"pending", "lost"})
        ~> operations[operation].response = "delivered"

====
