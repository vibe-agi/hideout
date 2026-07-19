------------------------ MODULE ResourceLifecycle ------------------------
EXTENDS FiniteSets, Naturals

CONSTANTS Sessions, ResourceIDs

VARIABLES vmState,
          activeSessions,
          bindings,
          ownershipProved,
          gracePending

vars == <<vmState, activeSessions, bindings, ownershipProved, gracePending>>

VMStates == {"running", "stopped"}
BindingSet == [resource : ResourceIDs, owner : Sessions]

Init ==
    /\ vmState = "running"
    /\ activeSessions = {}
    /\ bindings = {}
    /\ ownershipProved = TRUE
    /\ gracePending = FALSE

StartSession(session) ==
    /\ session \notin activeSessions
    /\ activeSessions' = activeSessions \cup {session}
    /\ vmState' = "running"
    /\ gracePending' = FALSE
    /\ UNCHANGED <<bindings, ownershipProved>>

EndSession(session) ==
    /\ session \in activeSessions
    /\ activeSessions' = activeSessions \ {session}
    /\ UNCHANGED <<vmState, bindings, ownershipProved, gracePending>>

AcquireResource(session, resource) ==
    /\ session \in activeSessions
    /\ ~\E binding \in bindings : binding.resource = resource
    /\ bindings' = bindings \cup {[resource |-> resource, owner |-> session]}
    /\ gracePending' = FALSE
    /\ UNCHANGED <<vmState, activeSessions, ownershipProved>>

ReleaseResource(session, resource) ==
    /\ [resource |-> resource, owner |-> session] \in bindings
    /\ bindings' = bindings \ {[resource |-> resource, owner |-> session]}
    /\ UNCHANGED <<vmState, activeSessions, ownershipProved, gracePending>>

BeginGrace ==
    /\ vmState = "running"
    /\ activeSessions = {}
    /\ bindings = {}
    /\ ownershipProved
    /\ ~gracePending
    /\ gracePending' = TRUE
    /\ UNCHANGED <<vmState, activeSessions, bindings, ownershipProved>>

AutoStop ==
    /\ vmState = "running"
    /\ gracePending
    /\ activeSessions = {}
    /\ bindings = {}
    /\ ownershipProved
    /\ vmState' = "stopped"
    /\ gracePending' = FALSE
    /\ UNCHANGED <<activeSessions, bindings, ownershipProved>>

LoseOwnershipProof ==
    /\ vmState = "running"
    /\ ownershipProved
    /\ ownershipProved' = FALSE
    /\ gracePending' = FALSE
    /\ UNCHANGED <<vmState, activeSessions, bindings>>

RecoverOwnershipProof ==
    /\ ~ownershipProved
    /\ ownershipProved' = TRUE
    /\ UNCHANGED <<vmState, activeSessions, bindings, gracePending>>

Next ==
    \/ \E session \in Sessions : StartSession(session)
    \/ \E session \in Sessions : EndSession(session)
    \/ \E session \in Sessions, resource \in ResourceIDs :
           AcquireResource(session, resource)
    \/ \E session \in Sessions, resource \in ResourceIDs :
           ReleaseResource(session, resource)
    \/ BeginGrace
    \/ AutoStop
    \/ LoseOwnershipProof
    \/ RecoverOwnershipProof

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ vmState \in VMStates
    /\ activeSessions \subseteq Sessions
    /\ bindings \subseteq BindingSet
    /\ ownershipProved \in BOOLEAN
    /\ gracePending \in BOOLEAN

StoppedIsQuiescent ==
    vmState = "stopped" => activeSessions = {} /\ bindings = {}

SingleResourceOwner ==
    \A resource \in ResourceIDs :
        Cardinality({binding \in bindings : binding.resource = resource}) <= 1

GraceIsProvedAndQuiescent ==
    gracePending =>
        vmState = "running" /\ activeSessions = {} /\ bindings = {} /\ ownershipProved

=============================================================================
