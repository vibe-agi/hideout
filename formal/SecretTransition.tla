-------------------- MODULE SecretTransition --------------------
EXTENDS Naturals, FiniteSets

CONSTANTS Clients, OperationIDs, Secrets, Connections,
          MaxSecretGeneration, MaxRetries

ASSUME /\ Clients # {}
       /\ OperationIDs # {}
       /\ Secrets # {}
       /\ Connections # {}
       /\ MaxSecretGeneration \in Nat \ {0}
       /\ MaxRetries \in Nat \ {0}

NoClient == "no-client"
NoOperation == "no-operation"
NoSecret == "no-secret"

TransitionPhases ==
    {"none", "planned", "staged", "probed", "activated", "proved",
     "drained", "provider-committed", "rolling-back",
     "recovery-required", "succeeded", "failed"}
ActiveTransitionPhases ==
    {"planned", "staged", "probed", "activated", "proved", "drained",
     "provider-committed", "rolling-back", "recovery-required"}
TerminalTransitionPhases == {"succeeded", "failed"}
ResponseStates == {"none", "pending", "lost", "delivered"}
ConnectionStates == {"closed", "open"}

RollbackUniverse ==
    [operation : OperationIDs,
     previousSecret : Secrets,
     failedSecret : Secrets,
     restoredSecret : Secrets,
     activationGeneration : 0..MaxSecretGeneration,
     rollbackGeneration : 0..MaxSecretGeneration]

VARIABLES activeSecret,
          activeGeneration,
          providerSecret,
          providerGeneration,
          routeAuthority,
          available,
          transitionOwner,
          operations,
          activationCount,
          rollbackCount,
          resetCount,
          connections,
          rollbackEvents,
          disconnected

vars ==
    <<activeSecret, activeGeneration, providerSecret, providerGeneration,
      routeAuthority, available, transitionOwner, operations, activationCount,
      rollbackCount, resetCount, connections, rollbackEvents, disconnected>>

EmptyOperation ==
    [phase |-> "none",
     client |-> NoClient,
     targetSecret |-> NoSecret,
     previousSecret |-> NoSecret,
     previousGeneration |-> 0,
     previousProviderGeneration |-> 0,
     targetWasAvailable |-> FALSE,
     staged |-> FALSE,
     probed |-> FALSE,
     switched |-> FALSE,
     routeProved |-> FALSE,
     drained |-> FALSE,
     providerWritten |-> FALSE,
     resetObserved |-> FALSE,
     response |-> "none",
     retries |-> 0]

ClosedConnection ==
    [state |-> "closed",
     secret |-> NoSecret,
     generation |-> 0]

Init ==
    /\ activeSecret \in Secrets
    /\ activeGeneration = 0
    /\ providerSecret = activeSecret
    /\ providerGeneration = 0
    /\ routeAuthority = TRUE
    /\ available =
        [secret \in Secrets |-> IF secret = activeSecret THEN TRUE ELSE FALSE]
    /\ transitionOwner = NoOperation
    /\ operations = [operation \in OperationIDs |-> EmptyOperation]
    /\ activationCount = [operation \in OperationIDs |-> 0]
    /\ rollbackCount = [operation \in OperationIDs |-> 0]
    /\ resetCount = [operation \in OperationIDs |-> 0]
    /\ connections = [connection \in Connections |-> ClosedConnection]
    /\ rollbackEvents = {}
    /\ disconnected = {}

Plan(client, operation, targetSecret) ==
    /\ client \in Clients \ disconnected
    /\ operation \in OperationIDs
    /\ targetSecret \in Secrets
    /\ targetSecret # activeSecret
    /\ routeAuthority
    /\ providerSecret = activeSecret
    /\ transitionOwner = NoOperation
    /\ operations[operation].phase = "none"
    /\ activeGeneration + 2 <= MaxSecretGeneration
    /\ providerGeneration < MaxSecretGeneration
    /\ transitionOwner' = operation
    /\ operations' =
        [operations EXCEPT
            ![operation] =
                [phase |-> "planned",
                 client |-> client,
                 targetSecret |-> targetSecret,
                 previousSecret |-> activeSecret,
                 previousGeneration |-> activeGeneration,
                 previousProviderGeneration |-> providerGeneration,
                 targetWasAvailable |-> available[targetSecret],
                 staged |-> FALSE,
                 probed |-> FALSE,
                 switched |-> FALSE,
                 routeProved |-> FALSE,
                 drained |-> FALSE,
                 providerWritten |-> FALSE,
                 resetObserved |-> FALSE,
                 response |-> "none",
                 retries |-> 0]]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    activationCount, rollbackCount, resetCount,
                    connections, rollbackEvents, disconnected>>

Stage(operation) ==
    LET target == operations[operation].targetSecret IN
    /\ transitionOwner = operation
    /\ operations[operation].phase = "planned"
    /\ routeAuthority
    /\ available' = [available EXCEPT ![target] = TRUE]
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "staged",
            ![operation].staged = TRUE]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, transitionOwner,
                    activationCount, rollbackCount, resetCount, connections,
                    rollbackEvents, disconnected>>

StageFailure(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "planned"
    /\ routeAuthority
    /\ operations' =
        [operations EXCEPT ![operation].phase = "rolling-back"]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

ProbeSuccess(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "staged"
    /\ routeAuthority
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "probed",
            ![operation].probed = TRUE]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

ProbeFailure(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "staged"
    /\ routeAuthority
    /\ operations' =
        [operations EXCEPT ![operation].phase = "rolling-back"]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

Activate(operation) ==
    LET target == operations[operation].targetSecret IN
    /\ transitionOwner = operation
    /\ operations[operation].phase = "probed"
    /\ routeAuthority
    /\ operations[operation].staged
    /\ operations[operation].probed
    /\ available[target]
    /\ activeGeneration < MaxSecretGeneration
    /\ activeSecret' = target
    /\ activeGeneration' = activeGeneration + 1
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "activated",
            ![operation].switched = TRUE]
    /\ activationCount' = [activationCount EXCEPT ![operation] = @ + 1]
    /\ UNCHANGED <<providerSecret, providerGeneration, routeAuthority,
                    available, transitionOwner, rollbackCount, resetCount,
                    connections, rollbackEvents, disconnected>>

ActivationFailure(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "probed"
    /\ routeAuthority
    /\ operations' =
        [operations EXCEPT ![operation].phase = "rolling-back"]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

ProveRoute(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "activated"
    /\ routeAuthority
    /\ operations[operation].switched
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "proved",
            ![operation].routeProved = TRUE]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

PostActivationFailure(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase \in {"activated", "proved", "drained"}
    /\ routeAuthority
    /\ ~operations[operation].providerWritten
    /\ operations' =
        [operations EXCEPT ![operation].phase = "rolling-back"]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

DrainRoute(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "proved"
    /\ routeAuthority
    /\ operations[operation].routeProved
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "drained",
            ![operation].drained = TRUE]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

CommitProvider(operation) ==
    LET target == operations[operation].targetSecret
        previous == operations[operation].previousSecret
        previousGeneration ==
            operations[operation].previousProviderGeneration
    IN
    /\ transitionOwner = operation
    /\ operations[operation].phase = "drained"
    /\ routeAuthority
    /\ operations[operation].staged
    /\ operations[operation].probed
    /\ operations[operation].switched
    /\ operations[operation].routeProved
    /\ operations[operation].drained
    /\ providerSecret = previous
    /\ providerGeneration = previousGeneration
    /\ providerGeneration < MaxSecretGeneration
    /\ providerSecret' = target
    /\ providerGeneration' = providerGeneration + 1
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "provider-committed",
            ![operation].providerWritten = TRUE]
    /\ UNCHANGED <<activeSecret, activeGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

Complete(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "provider-committed"
    /\ routeAuthority
    /\ operations[operation].providerWritten
    /\ providerSecret = operations[operation].targetSecret
    /\ providerGeneration =
        operations[operation].previousProviderGeneration + 1
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "succeeded",
            ![operation].response = "pending"]
    /\ transitionOwner' = NoOperation
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    activationCount, rollbackCount, resetCount, connections,
                    rollbackEvents, disconnected>>

ConnectionUsesSecret(secret) ==
    \E connection \in Connections :
        /\ connections[connection].state = "open"
        /\ connections[connection].secret = secret

RouteProofsComplete(operation) ==
    /\ operations[operation].staged
    /\ operations[operation].probed
    /\ operations[operation].switched
    /\ operations[operation].routeProved
    /\ operations[operation].drained

ProviderCommittedExact(operation) ==
    /\ operations[operation].providerWritten
    /\ providerSecret = operations[operation].targetSecret
    /\ providerGeneration =
        operations[operation].previousProviderGeneration + 1

ProviderUncommittedExact(operation) ==
    /\ ~operations[operation].providerWritten
    /\ providerSecret = operations[operation].previousSecret
    /\ providerGeneration =
        operations[operation].previousProviderGeneration

ProviderMismatched(operation) ==
    /\ ~ProviderCommittedExact(operation)
    /\ ~ProviderUncommittedExact(operation)

Rollback(operation) ==
    LET target == operations[operation].targetSecret
        previous == operations[operation].previousSecret
        switched == operations[operation].switched
        targetNeeded ==
            operations[operation].targetWasAvailable \/
            ConnectionUsesSecret(target)
    IN
    /\ transitionOwner = operation
    /\ operations[operation].phase = "rolling-back"
    /\ routeAuthority
    /\ ProviderUncommittedExact(operation)
    /\ switched => activeGeneration < MaxSecretGeneration
    /\ activeSecret' = IF switched THEN previous ELSE activeSecret
    /\ activeGeneration' =
        IF switched THEN activeGeneration + 1 ELSE activeGeneration
    /\ available' = [available EXCEPT ![target] = targetNeeded]
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "failed",
            ![operation].response = "pending"]
    /\ rollbackCount' = [rollbackCount EXCEPT ![operation] = @ + 1]
    /\ rollbackEvents' =
        IF switched
        THEN
            rollbackEvents \cup
                {[operation |-> operation,
                  previousSecret |-> previous,
                  failedSecret |-> target,
                  restoredSecret |-> previous,
                  activationGeneration |-> activeGeneration,
                  rollbackGeneration |-> activeGeneration + 1]}
        ELSE rollbackEvents
    /\ transitionOwner' = NoOperation
    /\ UNCHANGED <<providerSecret, providerGeneration, routeAuthority,
                    activationCount, resetCount, connections, disconnected>>

ProviderDrift(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase \in
        ActiveTransitionPhases \ {"provider-committed", "recovery-required"}
    /\ ~operations[operation].providerWritten
    /\ providerGeneration < MaxSecretGeneration
    /\ providerGeneration' = providerGeneration + 1
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    routeAuthority, available, transitionOwner, operations,
                    activationCount, rollbackCount, resetCount, connections,
                    rollbackEvents, disconnected>>

RequireProviderRecovery(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase \in
        ActiveTransitionPhases \ {"recovery-required"}
    /\ ProviderMismatched(operation)
    /\ operations' =
        [operations EXCEPT ![operation].phase = "recovery-required"]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

CrashNetworkAuthority(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase \in
        {"staged", "probed", "activated", "proved", "drained",
         "provider-committed", "rolling-back"}
    /\ routeAuthority
    /\ resetCount[operation] = 0
    /\ routeAuthority' = FALSE
    /\ connections' =
        [connection \in Connections |-> ClosedConnection]
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "recovery-required",
            ![operation].resetObserved = TRUE]
    /\ resetCount' = [resetCount EXCEPT ![operation] = @ + 1]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, available, transitionOwner,
                    activationCount, rollbackCount, rollbackEvents,
                    disconnected>>

RecoverCommittedAfterReset(operation) ==
    /\ transitionOwner = operation
    /\ operations[operation].phase = "recovery-required"
    /\ operations[operation].resetObserved
    /\ ~routeAuthority
    /\ ProviderCommittedExact(operation)
    /\ RouteProofsComplete(operation)
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "succeeded",
            ![operation].response = "pending"]
    /\ transitionOwner' = NoOperation
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    activationCount, rollbackCount, resetCount, connections,
                    rollbackEvents, disconnected>>

RecoverUncommittedAfterReset(operation) ==
    LET target == operations[operation].targetSecret IN
    /\ transitionOwner = operation
    /\ operations[operation].phase = "recovery-required"
    /\ operations[operation].resetObserved
    /\ ~routeAuthority
    /\ ProviderUncommittedExact(operation)
    /\ available' =
        [available EXCEPT
            ![target] = operations[operation].targetWasAvailable]
    /\ operations' =
        [operations EXCEPT
            ![operation].phase = "failed",
            ![operation].response = "pending"]
    /\ transitionOwner' = NoOperation
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, activationCount,
                    rollbackCount, resetCount, connections, rollbackEvents,
                    disconnected>>

AttachAfterReset ==
    /\ transitionOwner = NoOperation
    /\ ~routeAuthority
    /\ available[providerSecret]
    /\ activeGeneration < MaxSecretGeneration
    /\ activeSecret' = providerSecret
    /\ activeGeneration' = activeGeneration + 1
    /\ routeAuthority' = TRUE
    /\ UNCHANGED <<providerSecret, providerGeneration, available,
                    transitionOwner, operations, activationCount,
                    rollbackCount, resetCount, connections, rollbackEvents,
                    disconnected>>

OpenConnection(connection) ==
    /\ connection \in Connections
    /\ connections[connection].state = "closed"
    /\ routeAuthority
    /\ available[activeSecret]
    /\ connections' =
        [connections EXCEPT
            ![connection] =
                [state |-> "open",
                 secret |-> activeSecret,
                 generation |-> activeGeneration]]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, operations, activationCount,
                    rollbackCount, resetCount, rollbackEvents, disconnected>>

CloseConnection(connection) ==
    /\ connection \in Connections
    /\ connections[connection].state = "open"
    /\ connections' = [connections EXCEPT ![connection] = ClosedConnection]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, operations, activationCount,
                    rollbackCount, resetCount, rollbackEvents, disconnected>>

DeliverResponse(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].phase \in TerminalTransitionPhases
    /\ operations[operation].response = "pending"
    /\ operations' = [operations EXCEPT ![operation].response = "delivered"]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

LoseResponse(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].phase \in TerminalTransitionPhases
    /\ operations[operation].response = "pending"
    /\ operations' = [operations EXCEPT ![operation].response = "lost"]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

RetryExact(operation) ==
    /\ operation \in OperationIDs
    /\ operations[operation].phase \in TerminalTransitionPhases
    /\ operations[operation].response = "lost"
    /\ operations[operation].retries < MaxRetries
    /\ operations' =
        [operations EXCEPT
            ![operation].response = "delivered",
            ![operation].retries = @ + 1]
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, activationCount, rollbackCount,
                    resetCount, connections, rollbackEvents, disconnected>>

Disconnect(client) ==
    /\ client \in Clients \ disconnected
    /\ disconnected' = disconnected \cup {client}
    /\ UNCHANGED <<activeSecret, activeGeneration, providerSecret,
                    providerGeneration, routeAuthority, available,
                    transitionOwner, operations, activationCount,
                    rollbackCount, resetCount, connections, rollbackEvents>>

ResolveStage(operation) ==
    Stage(operation) \/ StageFailure(operation)

ResolveProbe(operation) ==
    ProbeSuccess(operation) \/ ProbeFailure(operation)

ResolveActivation(operation) ==
    Activate(operation) \/ ActivationFailure(operation)

ResolveActivated(operation) ==
    ProveRoute(operation) \/ PostActivationFailure(operation)

ResolveProved(operation) ==
    DrainRoute(operation) \/ PostActivationFailure(operation)

ResolveDrained(operation) ==
    CommitProvider(operation) \/ PostActivationFailure(operation)

ResolveReset(operation) ==
    RecoverCommittedAfterReset(operation) \/
    RecoverUncommittedAfterReset(operation)

Idle == UNCHANGED vars

Next ==
    \/ \E client \in Clients, operation \in OperationIDs,
          secret \in Secrets : Plan(client, operation, secret)
    \/ \E operation \in OperationIDs : Stage(operation)
    \/ \E operation \in OperationIDs : StageFailure(operation)
    \/ \E operation \in OperationIDs : ProbeSuccess(operation)
    \/ \E operation \in OperationIDs : ProbeFailure(operation)
    \/ \E operation \in OperationIDs : Activate(operation)
    \/ \E operation \in OperationIDs : ActivationFailure(operation)
    \/ \E operation \in OperationIDs : ProveRoute(operation)
    \/ \E operation \in OperationIDs : DrainRoute(operation)
    \/ \E operation \in OperationIDs : CommitProvider(operation)
    \/ \E operation \in OperationIDs : Complete(operation)
    \/ \E operation \in OperationIDs : PostActivationFailure(operation)
    \/ \E operation \in OperationIDs : Rollback(operation)
    \/ \E operation \in OperationIDs : ProviderDrift(operation)
    \/ \E operation \in OperationIDs : RequireProviderRecovery(operation)
    \/ \E operation \in OperationIDs : CrashNetworkAuthority(operation)
    \/ \E operation \in OperationIDs :
          RecoverCommittedAfterReset(operation)
    \/ \E operation \in OperationIDs :
          RecoverUncommittedAfterReset(operation)
    \/ AttachAfterReset
    \/ \E connection \in Connections : OpenConnection(connection)
    \/ \E connection \in Connections : CloseConnection(connection)
    \/ \E operation \in OperationIDs : DeliverResponse(operation)
    \/ \E operation \in OperationIDs : LoseResponse(operation)
    \/ \E operation \in OperationIDs : RetryExact(operation)
    \/ \E client \in Clients : Disconnect(client)
    \/ Idle

TransitionFairness ==
    /\ \A operation \in OperationIDs : WF_vars(ResolveStage(operation))
    /\ \A operation \in OperationIDs : WF_vars(ResolveProbe(operation))
    /\ \A operation \in OperationIDs : WF_vars(ResolveActivation(operation))
    /\ \A operation \in OperationIDs : WF_vars(ResolveActivated(operation))
    /\ \A operation \in OperationIDs : WF_vars(ResolveProved(operation))
    /\ \A operation \in OperationIDs : WF_vars(ResolveDrained(operation))
    /\ \A operation \in OperationIDs : WF_vars(Complete(operation))
    /\ \A operation \in OperationIDs : WF_vars(Rollback(operation))
    /\ \A operation \in OperationIDs :
          WF_vars(RequireProviderRecovery(operation))
    /\ \A operation \in OperationIDs : WF_vars(ResolveReset(operation))

ResponseFairness ==
    /\ \A operation \in OperationIDs : WF_vars(DeliverResponse(operation))
    /\ \A operation \in OperationIDs : WF_vars(RetryExact(operation))

Spec == Init /\ [][Next]_vars /\ TransitionFairness /\ ResponseFairness

TypeOK ==
    /\ activeSecret \in Secrets
    /\ activeGeneration \in 0..MaxSecretGeneration
    /\ providerSecret \in Secrets
    /\ providerGeneration \in 0..MaxSecretGeneration
    /\ routeAuthority \in BOOLEAN
    /\ available \in [Secrets -> BOOLEAN]
    /\ transitionOwner \in OperationIDs \cup {NoOperation}
    /\ operations \in
        [OperationIDs ->
            [phase : TransitionPhases,
             client : Clients \cup {NoClient},
             targetSecret : Secrets \cup {NoSecret},
             previousSecret : Secrets \cup {NoSecret},
             previousGeneration : 0..MaxSecretGeneration,
             previousProviderGeneration : 0..MaxSecretGeneration,
             targetWasAvailable : BOOLEAN,
             staged : BOOLEAN,
             probed : BOOLEAN,
             switched : BOOLEAN,
             routeProved : BOOLEAN,
             drained : BOOLEAN,
             providerWritten : BOOLEAN,
             resetObserved : BOOLEAN,
             response : ResponseStates,
             retries : 0..MaxRetries]]
    /\ activationCount \in [OperationIDs -> 0..1]
    /\ rollbackCount \in [OperationIDs -> 0..1]
    /\ resetCount \in [OperationIDs -> 0..1]
    /\ connections \in
        [Connections ->
            [state : ConnectionStates,
             secret : Secrets \cup {NoSecret},
             generation : 0..MaxSecretGeneration]]
    /\ rollbackEvents \subseteq RollbackUniverse
    /\ disconnected \subseteq Clients

ActiveAndConnectedSecretsRemainAvailable ==
    /\ available[providerSecret]
    /\ routeAuthority => available[activeSecret]
    /\ \A connection \in Connections :
          connections[connection].state = "open" =>
              /\ routeAuthority
              /\ connections[connection].secret \in Secrets
              /\ available[connections[connection].secret]
              /\ connections[connection].generation <= activeGeneration

ActivationRequiresSuccessfulProbe ==
    \A operation \in OperationIDs :
        operations[operation].switched =>
            /\ operations[operation].staged
            /\ operations[operation].probed
            /\ activationCount[operation] = 1

ProviderCommitRequiresCompleteRouteProofs ==
    \A operation \in OperationIDs :
        operations[operation].providerWritten =>
            /\ RouteProofsComplete(operation)
            /\ ProviderCommittedExact(operation)

NetworkAuthorityResetClosesConnections ==
    ~routeAuthority =>
        \A connection \in Connections :
            connections[connection].state = "closed"

ResetRecoveryIsExact ==
    \A operation \in OperationIDs :
        /\ (operations[operation].resetObserved /\
            operations[operation].phase = "succeeded") =>
              /\ resetCount[operation] = 1
              /\ ProviderCommittedExact(operation)
              /\ RouteProofsComplete(operation)
        /\ (operations[operation].resetObserved /\
            operations[operation].phase = "failed") =>
              /\ resetCount[operation] = 1
              /\ ProviderUncommittedExact(operation)
        /\ (operations[operation].resetObserved /\
            ProviderMismatched(operation)) =>
              operations[operation].phase = "recovery-required"

AtMostOneActivationAndRollback ==
    \A operation \in OperationIDs :
        /\ activationCount[operation] <= 1
        /\ rollbackCount[operation] <= 1

ExclusiveTransitionOwner ==
    /\ transitionOwner = NoOperation =>
          \A operation \in OperationIDs :
              operations[operation].phase \notin ActiveTransitionPhases
    /\ transitionOwner \in OperationIDs =>
          /\ operations[transitionOwner].phase \in ActiveTransitionPhases
          /\ \A operation \in OperationIDs \ {transitionOwner} :
                operations[operation].phase \notin ActiveTransitionPhases

RollbackRestoresPreviousRoute ==
    \A event \in rollbackEvents :
        /\ event.restoredSecret = event.previousSecret
        /\ event.rollbackGeneration = event.activationGeneration + 1
        /\ operations[event.operation].previousSecret = event.previousSecret
        /\ operations[event.operation].targetSecret = event.failedSecret
        /\ operations[event.operation].phase = "failed"
        /\ rollbackCount[event.operation] = 1

ConnectionBindingStep ==
    \A connection \in Connections :
        /\ connections[connection].state = "open"
        /\ connections'[connection].state = "open"
        =>
            /\ connections'[connection].secret =
                  connections[connection].secret
            /\ connections'[connection].generation =
                  connections[connection].generation

ExistingConnectionBindingPreserved ==
    [][ConnectionBindingStep]_vars

TransitionEventuallySettled ==
    \A operation \in OperationIDs :
        (operations[operation].phase \in
            ActiveTransitionPhases \ {"recovery-required"})
            ~> (operations[operation].phase \in
                TerminalTransitionPhases \cup {"recovery-required"})

ExactResetRecoveryEventuallyTerminal ==
    \A operation \in OperationIDs :
        /\ operations[operation].phase = "recovery-required"
        /\ operations[operation].resetObserved
        /\ (ProviderCommittedExact(operation) \/
            ProviderUncommittedExact(operation))
        ~> operations[operation].phase \in TerminalTransitionPhases

SecretResponseEventuallyDelivered ==
    \A operation \in OperationIDs :
        /\ (operations[operation].phase \in TerminalTransitionPhases)
        /\ (operations[operation].response \in {"pending", "lost"})
        ~> operations[operation].response = "delivered"

====
