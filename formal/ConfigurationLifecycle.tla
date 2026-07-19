--------------------- MODULE ConfigurationLifecycle ---------------------
EXTENDS Naturals

CONSTANTS Values, Sessions, MaxGeneration

VARIABLES effective,
          desired,
          phase,
          pendingLayer,
          candidate,
          machineGeneration,
          bootGeneration,
          serviceGeneration,
          sessionSnapshots,
          snapshotWritten,
          lastCommit,
          infrastructureAtLastCommit

vars == <<effective, desired, phase, pendingLayer, candidate,
          machineGeneration, bootGeneration, serviceGeneration,
          sessionSnapshots, snapshotWritten, lastCommit,
          infrastructureAtLastCommit>>

Layers == {"machine", "boot", "service", "session"}
Phases == {"idle", "pending", "failed"}
Commits == Layers \cup {"none"}
NoValue == "none"

Init ==
    \E initial \in Values :
        /\ effective = [machine |-> initial,
                        boot |-> initial,
                        service |-> initial,
                        session |-> initial]
        /\ desired = effective
        /\ phase = "idle"
        /\ pendingLayer = "none"
        /\ candidate = NoValue
        /\ machineGeneration = 0
        /\ bootGeneration = 0
        /\ serviceGeneration = 0
        /\ sessionSnapshots = [session \in Sessions |-> NoValue]
        /\ snapshotWritten = {}
        /\ lastCommit = "none"
        /\ infrastructureAtLastCommit =
              [machine |-> 0, boot |-> 0, service |-> 0]

RequestChange(layer, value) ==
    /\ phase = "idle"
    /\ layer \in Layers
    /\ value \in Values
    /\ value # effective[layer]
    /\ phase' = "pending"
    /\ pendingLayer' = layer
    /\ candidate' = value
    /\ desired' = [desired EXCEPT ![layer] = value]
    /\ UNCHANGED <<effective, machineGeneration, bootGeneration,
                    serviceGeneration, sessionSnapshots, snapshotWritten,
                    lastCommit, infrastructureAtLastCommit>>

CommitMachine ==
    /\ phase = "pending"
    /\ pendingLayer = "machine"
    /\ machineGeneration < MaxGeneration
    /\ effective' = [effective EXCEPT !.machine = candidate]
    /\ machineGeneration' = machineGeneration + 1
    /\ phase' = "idle"
    /\ pendingLayer' = "none"
    /\ candidate' = NoValue
    /\ lastCommit' = "machine"
    /\ infrastructureAtLastCommit' =
          [machine |-> machineGeneration + 1,
           boot |-> bootGeneration,
           service |-> serviceGeneration]
    /\ UNCHANGED <<desired, bootGeneration, serviceGeneration,
                    sessionSnapshots, snapshotWritten>>

CommitBoot ==
    /\ phase = "pending"
    /\ pendingLayer = "boot"
    /\ bootGeneration < MaxGeneration
    /\ effective' = [effective EXCEPT !.boot = candidate]
    /\ bootGeneration' = bootGeneration + 1
    /\ phase' = "idle"
    /\ pendingLayer' = "none"
    /\ candidate' = NoValue
    /\ lastCommit' = "boot"
    /\ infrastructureAtLastCommit' =
          [machine |-> machineGeneration,
           boot |-> bootGeneration + 1,
           service |-> serviceGeneration]
    /\ UNCHANGED <<desired, machineGeneration, serviceGeneration,
                    sessionSnapshots, snapshotWritten>>

CommitService ==
    /\ phase = "pending"
    /\ pendingLayer = "service"
    /\ serviceGeneration < MaxGeneration
    /\ effective' = [effective EXCEPT !.service = candidate]
    /\ serviceGeneration' = serviceGeneration + 1
    /\ phase' = "idle"
    /\ pendingLayer' = "none"
    /\ candidate' = NoValue
    /\ lastCommit' = "service"
    /\ infrastructureAtLastCommit' =
          [machine |-> machineGeneration,
           boot |-> bootGeneration,
           service |-> serviceGeneration + 1]
    /\ UNCHANGED <<desired, machineGeneration, bootGeneration,
                    sessionSnapshots, snapshotWritten>>

CommitSession ==
    /\ phase = "pending"
    /\ pendingLayer = "session"
    /\ effective' = [effective EXCEPT !.session = candidate]
    /\ phase' = "idle"
    /\ pendingLayer' = "none"
    /\ candidate' = NoValue
    /\ lastCommit' = "session"
    /\ infrastructureAtLastCommit' =
          [machine |-> machineGeneration,
           boot |-> bootGeneration,
           service |-> serviceGeneration]
    /\ UNCHANGED <<desired, machineGeneration, bootGeneration,
                    serviceGeneration, sessionSnapshots, snapshotWritten>>

Rollback ==
    /\ phase = "pending"
    /\ desired' = [desired EXCEPT ![pendingLayer] = effective[pendingLayer]]
    /\ phase' = "idle"
    /\ pendingLayer' = "none"
    /\ candidate' = NoValue
    /\ lastCommit' = "none"
    /\ UNCHANGED <<effective, machineGeneration, bootGeneration,
                    serviceGeneration, sessionSnapshots, snapshotWritten,
                    infrastructureAtLastCommit>>

FailClosed ==
    /\ phase = "pending"
    /\ phase' = "failed"
    /\ lastCommit' = "none"
    /\ UNCHANGED <<effective, desired, pendingLayer, candidate,
                    machineGeneration, bootGeneration, serviceGeneration,
                    sessionSnapshots, snapshotWritten,
                    infrastructureAtLastCommit>>

Recover ==
    /\ phase = "failed"
    /\ desired' = effective
    /\ phase' = "idle"
    /\ pendingLayer' = "none"
    /\ candidate' = NoValue
    /\ UNCHANGED <<effective, machineGeneration, bootGeneration,
                    serviceGeneration, sessionSnapshots, snapshotWritten,
                    lastCommit, infrastructureAtLastCommit>>

StartSession(session) ==
    /\ phase = "idle"
    /\ session \notin snapshotWritten
    /\ sessionSnapshots' =
          [sessionSnapshots EXCEPT ![session] = effective.session]
    /\ snapshotWritten' = snapshotWritten \cup {session}
    /\ UNCHANGED <<effective, desired, phase, pendingLayer, candidate,
                    machineGeneration, bootGeneration, serviceGeneration,
                    lastCommit, infrastructureAtLastCommit>>

Next ==
    \/ \E layer \in Layers, value \in Values : RequestChange(layer, value)
    \/ CommitMachine
    \/ CommitBoot
    \/ CommitService
    \/ CommitSession
    \/ Rollback
    \/ FailClosed
    \/ Recover
    \/ \E session \in Sessions : StartSession(session)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ effective \in [Layers -> Values]
    /\ desired \in [Layers -> Values]
    /\ phase \in Phases
    /\ pendingLayer \in Layers \cup {"none"}
    /\ candidate \in Values \cup {NoValue}
    /\ machineGeneration \in 0..MaxGeneration
    /\ bootGeneration \in 0..MaxGeneration
    /\ serviceGeneration \in 0..MaxGeneration
    /\ sessionSnapshots \in [Sessions -> Values \cup {NoValue}]
    /\ snapshotWritten \subseteq Sessions
    /\ lastCommit \in Commits

WrittenSnapshotsAreConcrete ==
    \A session \in Sessions :
        (session \in snapshotWritten) <=> (sessionSnapshots[session] \in Values)

LiveCommitPreservesMachineAndBoot ==
    lastCommit = "service" =>
        /\ machineGeneration = infrastructureAtLastCommit.machine
        /\ bootGeneration = infrastructureAtLastCommit.boot

SessionCommitPreservesInfrastructure ==
    lastCommit = "session" =>
        /\ machineGeneration = infrastructureAtLastCommit.machine
        /\ bootGeneration = infrastructureAtLastCommit.boot
        /\ serviceGeneration = infrastructureAtLastCommit.service

=============================================================================
