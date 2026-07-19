------------------------ MODULE NetworkTransition ------------------------
EXTENDS Naturals

CONSTANTS Sessions, Connections

VARIABLES route,
          candidate,
          previous,
          routeAtStage,
          phase,
          postureSwitch,
          activeSessions,
          connectionRoutes

vars == <<route, candidate, previous, routeAtStage, phase, postureSwitch,
          activeSessions, connectionRoutes>>

Routes == {"direct", "proxy-a", "proxy-b"}
NoRoute == "none"
Phases == {"idle", "staged", "activated"}

Posture(value) == IF value = "direct" THEN "direct" ELSE "proxy"

Init ==
    /\ route = "direct"
    /\ candidate = NoRoute
    /\ previous = NoRoute
    /\ routeAtStage = "direct"
    /\ phase = "idle"
    /\ postureSwitch = FALSE
    /\ activeSessions = {}
    /\ connectionRoutes = [connection \in Connections |-> NoRoute]

StartSession(session) ==
    /\ session \notin activeSessions
    /\ ~(phase # "idle" /\ postureSwitch)
    /\ activeSessions' = activeSessions \cup {session}
    /\ UNCHANGED <<route, candidate, previous, routeAtStage, phase,
                    postureSwitch, connectionRoutes>>

EndSession(session) ==
    /\ session \in activeSessions
    /\ activeSessions' = activeSessions \ {session}
    /\ UNCHANGED <<route, candidate, previous, routeAtStage, phase,
                    postureSwitch, connectionRoutes>>

Stage(nextRoute) ==
    /\ phase = "idle"
    /\ nextRoute \in Routes
    /\ nextRoute # route
    /\ (Posture(nextRoute) = Posture(route) \/ activeSessions = {})
    /\ candidate' = nextRoute
    /\ previous' = route
    /\ routeAtStage' = route
    /\ phase' = "staged"
    /\ postureSwitch' = (Posture(nextRoute) # Posture(route))
    /\ UNCHANGED <<route, activeSessions, connectionRoutes>>

Activate ==
    /\ phase = "staged"
    /\ route' = candidate
    /\ phase' = "activated"
    /\ UNCHANGED <<candidate, previous, routeAtStage, postureSwitch,
                    activeSessions, connectionRoutes>>

Commit ==
    /\ phase = "activated"
    /\ phase' = "idle"
    /\ candidate' = NoRoute
    /\ previous' = NoRoute
    /\ routeAtStage' = route
    /\ postureSwitch' = FALSE
    /\ UNCHANGED <<route, activeSessions, connectionRoutes>>

Rollback ==
    /\ phase \in {"staged", "activated"}
    /\ route' = previous
    /\ phase' = "idle"
    /\ candidate' = NoRoute
    /\ previous' = NoRoute
    /\ routeAtStage' = previous
    /\ postureSwitch' = FALSE
    /\ UNCHANGED <<activeSessions, connectionRoutes>>

AcceptConnection(connection) ==
    /\ connectionRoutes[connection] = NoRoute
    /\ connectionRoutes' = [connectionRoutes EXCEPT ![connection] = route]
    /\ UNCHANGED <<route, candidate, previous, routeAtStage, phase,
                    postureSwitch, activeSessions>>

CloseConnection(connection) ==
    /\ connectionRoutes[connection] \in Routes
    /\ connectionRoutes' = [connectionRoutes EXCEPT ![connection] = NoRoute]
    /\ UNCHANGED <<route, candidate, previous, routeAtStage, phase,
                    postureSwitch, activeSessions>>

Next ==
    \/ \E session \in Sessions : StartSession(session)
    \/ \E session \in Sessions : EndSession(session)
    \/ \E nextRoute \in Routes : Stage(nextRoute)
    \/ Activate
    \/ Commit
    \/ Rollback
    \/ \E connection \in Connections : AcceptConnection(connection)
    \/ \E connection \in Connections : CloseConnection(connection)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ route \in Routes
    /\ candidate \in Routes \cup {NoRoute}
    /\ previous \in Routes \cup {NoRoute}
    /\ routeAtStage \in Routes
    /\ phase \in Phases
    /\ postureSwitch \in BOOLEAN
    /\ activeSessions \subseteq Sessions
    /\ connectionRoutes \in [Connections -> Routes \cup {NoRoute}]

StageDoesNotChangeTraffic ==
    phase = "staged" => route = routeAtStage

CrossPostureTransitionIsQuiescent ==
    postureSwitch => activeSessions = {}

ActivatedRouteIsCandidate ==
    phase = "activated" => route = candidate

=============================================================================
