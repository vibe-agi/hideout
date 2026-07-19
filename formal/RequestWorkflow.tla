------------------------- MODULE RequestWorkflow -------------------------
EXTENDS Naturals

CONSTANTS Requests, Clients, MaxTime

VARIABLES states,
          claimants,
          sessionAlive,
          deadlines,
          now

vars == <<states, claimants, sessionAlive, deadlines, now>>

States == {"absent", "pending", "claimed", "approved", "denied"}
TerminalStates == {"approved", "denied"}
NoClient == "none"

Init ==
    /\ states = [request \in Requests |-> "absent"]
    /\ claimants = [request \in Requests |-> NoClient]
    /\ sessionAlive = [request \in Requests |-> TRUE]
    /\ deadlines = [request \in Requests |-> 0]
    /\ now = 0

NextDeadline == IF now < MaxTime THEN now + 1 ELSE MaxTime

Create(request) ==
    /\ states[request] = "absent"
    /\ sessionAlive[request]
    /\ states' = [states EXCEPT ![request] = "pending"]
    /\ deadlines' = [deadlines EXCEPT ![request] = NextDeadline]
    /\ UNCHANGED <<claimants, sessionAlive, now>>

Claim(request, client) ==
    /\ states[request] = "pending"
    /\ now < deadlines[request]
    /\ states' = [states EXCEPT ![request] = "claimed"]
    /\ claimants' = [claimants EXCEPT ![request] = client]
    /\ UNCHANGED <<sessionAlive, deadlines, now>>

Approve(request, client) ==
    /\ states[request] = "claimed"
    /\ claimants[request] = client
    /\ now < deadlines[request]
    /\ states' = [states EXCEPT ![request] = "approved"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<sessionAlive, deadlines, now>>

Deny(request, client) ==
    /\ states[request] = "claimed"
    /\ claimants[request] = client
    /\ states' = [states EXCEPT ![request] = "denied"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<sessionAlive, deadlines, now>>

Timeout(request) ==
    /\ states[request] \in {"pending", "claimed"}
    /\ now >= deadlines[request]
    /\ states' = [states EXCEPT ![request] = "denied"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<sessionAlive, deadlines, now>>

Reopen(request) ==
    /\ states[request] \in TerminalStates
    /\ sessionAlive[request]
    /\ states' = [states EXCEPT ![request] = "pending"]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ deadlines' = [deadlines EXCEPT ![request] = NextDeadline]
    /\ UNCHANGED <<sessionAlive, now>>

EndSession(request) ==
    /\ sessionAlive[request]
    /\ sessionAlive' = [sessionAlive EXCEPT ![request] = FALSE]
    /\ states' =
          [states EXCEPT ![request] =
              IF @ \in {"pending", "claimed"} THEN "denied" ELSE @]
    /\ claimants' = [claimants EXCEPT ![request] = NoClient]
    /\ UNCHANGED <<deadlines, now>>

Tick ==
    /\ now < MaxTime
    /\ now' = now + 1
    /\ UNCHANGED <<states, claimants, sessionAlive, deadlines>>

Next ==
    \/ \E request \in Requests : Create(request)
    \/ \E request \in Requests, client \in Clients : Claim(request, client)
    \/ \E request \in Requests, client \in Clients : Approve(request, client)
    \/ \E request \in Requests, client \in Clients : Deny(request, client)
    \/ \E request \in Requests : Timeout(request)
    \/ \E request \in Requests : Reopen(request)
    \/ \E request \in Requests : EndSession(request)
    \/ Tick

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ states \in [Requests -> States]
    /\ claimants \in [Requests -> Clients \cup {NoClient}]
    /\ sessionAlive \in [Requests -> BOOLEAN]
    /\ deadlines \in [Requests -> 0..MaxTime]
    /\ now \in 0..MaxTime

ClaimedHasExactlyOneClaimant ==
    \A request \in Requests :
        (states[request] = "claimed") <=> (claimants[request] \in Clients)

TerminalHasNoClaimant ==
    \A request \in Requests :
        states[request] \in TerminalStates => claimants[request] = NoClient

EndedSessionHasNoPendingAuthority ==
    \A request \in Requests :
        ~sessionAlive[request] => states[request] \notin {"pending", "claimed"}

=============================================================================
