------------------------- MODULE RequestWorkflow -------------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Requests, Clients, MaxTime

VARIABLES states,
          claimants,
          sessionAlive,
          deadlines,
          now,
          disconnected,
          cleanup,
          retryUsed,
          daemonUp,
          crashUsed

vars ==
    <<states, claimants, sessionAlive, deadlines, now, disconnected, cleanup,
      retryUsed, daemonUp, crashUsed>>

States == {"absent", "pending", "claimed", "approved", "denied"}
TerminalStates == {"approved", "denied"}
CleanupStates == {"none", "pending", "completed"}
NoClient == "none"

Symmetry == Permutations(Requests) \cup Permutations(Clients)

Init ==
    /\ states = [request \in Requests |-> "absent"]
    /\ claimants = [request \in Requests |-> NoClient]
    /\ sessionAlive = [request \in Requests |-> TRUE]
    /\ deadlines = [request \in Requests |-> 0]
    /\ now = 0
    /\ disconnected = {}
    /\ cleanup = [request \in Requests |-> "none"]
    /\ retryUsed = [request \in Requests |-> FALSE]
    /\ daemonUp = TRUE
    /\ crashUsed = FALSE

NextDeadline == IF now < MaxTime THEN now + 1 ELSE MaxTime

Create(request) ==
    /\ daemonUp
    /\ states[request] = "absent"
    /\ sessionAlive[request]
    /\ states' = [states EXCEPT ![request] = "pending"]
    /\ deadlines' = [deadlines EXCEPT ![request] = NextDeadline]
    /\ UNCHANGED <<claimants, sessionAlive, now, disconnected, cleanup,
                    retryUsed, daemonUp, crashUsed>>

Claim(request, client) ==
    /\ daemonUp
    /\ client \in Clients \ disconnected
    /\ states[request] = "pending"
    /\ now < deadlines[request]
    /\ states' = [states EXCEPT ![request] = "claimed"]
    /\ claimants' = [claimants EXCEPT ![request] = client]
    /\ UNCHANGED <<sessionAlive, deadlines, now, disconnected, cleanup,
                    retryUsed, daemonUp, crashUsed>>

Approve(request, client) ==
    /\ daemonUp
    /\ client \in Clients \ disconnected
    /\ states[request] = "claimed"
    /\ claimants[request] = client
    /\ now < deadlines[request]
    /\ states' = [states EXCEPT ![request] = "approved"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<sessionAlive, deadlines, now, disconnected, cleanup,
                    retryUsed, daemonUp, crashUsed>>

Deny(request, client) ==
    /\ daemonUp
    /\ client \in Clients \ disconnected
    /\ states[request] = "claimed"
    /\ claimants[request] = client
    /\ states' = [states EXCEPT ![request] = "denied"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<sessionAlive, deadlines, now, disconnected, cleanup,
                    retryUsed, daemonUp, crashUsed>>

Timeout(request) ==
    /\ daemonUp
    /\ states[request] \in {"pending", "claimed"}
    /\ now >= deadlines[request]
    /\ states' = [states EXCEPT ![request] = "denied"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<sessionAlive, deadlines, now, disconnected, cleanup,
                    retryUsed, daemonUp, crashUsed>>

\* Closing a dialog or losing its client releases the visible lease. Before
\* the request deadline another authenticated surface may take over; after the
\* deadline the same release is terminal.
ReleaseDisconnectedClaim(request) ==
    LET claimant == claimants[request] IN
    /\ daemonUp
    /\ states[request] = "claimed"
    /\ claimant \in disconnected
    /\ states' =
        [states EXCEPT
            ![request] =
                IF now < deadlines[request] THEN "pending" ELSE "denied"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<sessionAlive, deadlines, now, disconnected, cleanup,
                    retryUsed, daemonUp, crashUsed>>

\* A bounded retry models re-opening the same durable request rather than
\* minting a second authority record.
Retry(request) ==
    /\ daemonUp
    /\ states[request] \in TerminalStates
    /\ sessionAlive[request]
    /\ ~retryUsed[request]
    /\ states' = [states EXCEPT ![request] = "pending"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ deadlines' = [deadlines EXCEPT ![request] = NextDeadline]
    /\ retryUsed' = [retryUsed EXCEPT ![request] = TRUE]
    /\ UNCHANGED <<sessionAlive, now, disconnected, cleanup, daemonUp,
                    crashUsed>>

\* Ending the authoritative session persists cleanup intent and revokes any
\* pending or claimed authority in the same step.
EndSession(request) ==
    /\ daemonUp
    /\ sessionAlive[request]
    /\ sessionAlive' = [sessionAlive EXCEPT ![request] = FALSE]
    /\ states' =
          [states EXCEPT ![request] =
              IF @ \in {"pending", "claimed"} THEN "denied" ELSE @]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ cleanup' = [cleanup EXCEPT ![request] = "pending"]
    /\ UNCHANGED <<deadlines, now, disconnected, retryUsed, daemonUp,
                    crashUsed>>

CompleteCleanup(request) ==
    /\ daemonUp
    /\ ~sessionAlive[request]
    /\ cleanup[request] = "pending"
    /\ cleanup' = [cleanup EXCEPT ![request] = "completed"]
    /\ UNCHANGED <<states, claimants, sessionAlive, deadlines, now,
                    disconnected, retryUsed, daemonUp, crashUsed>>

Disconnect(client) ==
    /\ client \in Clients \ disconnected
    /\ disconnected' = disconnected \cup {client}
    /\ UNCHANGED <<states, claimants, sessionAlive, deadlines, now, cleanup,
                    retryUsed, daemonUp, crashUsed>>

\* As in OperatorConfiguration, one bounded crash covers every state boundary
\* while permitting a meaningful eventual-restart assumption.
Crash ==
    /\ daemonUp
    /\ ~crashUsed
    /\ daemonUp' = FALSE
    /\ crashUsed' = TRUE
    /\ UNCHANGED <<states, claimants, sessionAlive, deadlines, now,
                    disconnected, cleanup, retryUsed>>

Restart ==
    /\ ~daemonUp
    /\ daemonUp' = TRUE
    /\ UNCHANGED <<states, claimants, sessionAlive, deadlines, now,
                    disconnected, cleanup, retryUsed, crashUsed>>

\* Time is external to the daemon. Lease expiry therefore continues to become
\* observable during a crash and is reconciled after restart.
Tick ==
    /\ now < MaxTime
    /\ now' = now + 1
    /\ UNCHANGED <<states, claimants, sessionAlive, deadlines, disconnected,
                    cleanup, retryUsed, daemonUp, crashUsed>>

Idle == UNCHANGED vars

Next ==
    \/ \E request \in Requests : Create(request)
    \/ \E request \in Requests, client \in Clients : Claim(request, client)
    \/ \E request \in Requests, client \in Clients : Approve(request, client)
    \/ \E request \in Requests, client \in Clients : Deny(request, client)
    \/ \E request \in Requests : Timeout(request)
    \/ \E request \in Requests : ReleaseDisconnectedClaim(request)
    \/ \E request \in Requests : Retry(request)
    \/ \E request \in Requests : EndSession(request)
    \/ \E request \in Requests : CompleteCleanup(request)
    \/ \E client \in Clients : Disconnect(client)
    \/ Crash
    \/ Restart
    \/ Tick
    \/ Idle

LeaseFairness ==
    /\ WF_vars(Tick)
    /\ WF_vars(Restart)
    /\ \A request \in Requests : WF_vars(Timeout(request))
    /\ \A request \in Requests :
          WF_vars(ReleaseDisconnectedClaim(request))

CleanupFairness ==
    \A request \in Requests : WF_vars(CompleteCleanup(request))

SafetySpec == Init /\ [][Next]_vars

Spec == Init /\ [][Next]_vars /\ LeaseFairness /\ CleanupFairness

TypeOK ==
    /\ states \in [Requests -> States]
    /\ claimants \in [Requests -> Clients \cup {NoClient}]
    /\ sessionAlive \in [Requests -> BOOLEAN]
    /\ deadlines \in [Requests -> 0..MaxTime]
    /\ now \in 0..MaxTime
    /\ disconnected \subseteq Clients
    /\ cleanup \in [Requests -> CleanupStates]
    /\ retryUsed \in [Requests -> BOOLEAN]
    /\ daemonUp \in BOOLEAN
    /\ crashUsed \in BOOLEAN

ClaimedHasExactlyOneClaimant ==
    \A request \in Requests :
        (states[request] = "claimed") <=> (claimants[request] \in Clients)

TerminalHasNoClaimant ==
    \A request \in Requests :
        states[request] \in TerminalStates => claimants[request] = NoClient

EndedSessionHasNoPendingAuthority ==
    \A request \in Requests :
        ~sessionAlive[request] => states[request] \notin {"pending", "claimed"}

CleanupRequiresEndedSession ==
    \A request \in Requests :
        cleanup[request] \in {"pending", "completed"} => ~sessionAlive[request]

RequestEventuallyTerminal ==
    \A request \in Requests :
        (states[request] \in {"pending", "claimed"})
            ~> (states[request] \in TerminalStates)

EveryClaimEventuallyReleased ==
    \A request \in Requests :
        (states[request] = "claimed")
            ~> (claimants[request] = NoClient)

DisconnectedClaimEventuallyReleased ==
    \A request \in Requests :
        /\ states[request] = "claimed"
        /\ claimants[request] \in disconnected
        ~> claimants[request] = NoClient

CrashEventuallyRestarts ==
    (~daemonUp) ~> daemonUp

EndedSessionEventuallyClean ==
    \A request \in Requests :
        (~sessionAlive[request]) ~> (cleanup[request] = "completed")

=============================================================================
