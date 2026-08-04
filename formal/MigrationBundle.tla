------------------------- MODULE MigrationBundle -------------------------
EXTENDS Naturals

CONSTANTS MaxChunks, MaxCrashes, SourceDigest

ASSUME MaxChunks \in Nat /\ MaxChunks > 0
ASSUME MaxCrashes \in Nat

VARIABLES phase,
          sourceDigest,
          sourceStopped,
          claimHeld,
          snapshotExists,
          snapshotIndependent,
          written,
          checkpoint,
          tailAuthentic,
          footer,
          published,
          tampered,
          cancelRequested,
          retainPartial,
          partialRetained,
          daemonUp,
          crashCount,
          snapshotEffects,
          sealEffects

vars ==
    <<phase, sourceDigest, sourceStopped, claimHeld, snapshotExists,
      snapshotIndependent, written, checkpoint, tailAuthentic, footer,
      published, tampered, cancelRequested, retainPartial, partialRetained,
      daemonUp, crashCount,
      snapshotEffects, sealEffects>>

Phases == {"draft", "claimed", "snapshotted", "writing", "sealed",
           "cancelled"}
TerminalPhases == {"sealed", "cancelled"}

Importable ==
    /\ phase = "sealed"
    /\ footer
    /\ published
    /\ ~tampered
    /\ written = MaxChunks
    /\ checkpoint = MaxChunks
    /\ tailAuthentic

Init ==
    /\ phase = "draft"
    /\ sourceDigest = SourceDigest
    /\ sourceStopped \in BOOLEAN
    /\ claimHeld = FALSE
    /\ snapshotExists = FALSE
    /\ snapshotIndependent = FALSE
    /\ written = 0
    /\ checkpoint = 0
    /\ tailAuthentic = TRUE
    /\ footer = FALSE
    /\ published = FALSE
    /\ tampered = FALSE
    /\ cancelRequested = FALSE
    /\ retainPartial = FALSE
    /\ partialRetained = FALSE
    /\ daemonUp = TRUE
    /\ crashCount = 0
    /\ snapshotEffects = 0
    /\ sealEffects = 0

StopSource ==
    /\ daemonUp
    /\ phase = "draft"
    /\ ~sourceStopped
    /\ sourceStopped' = TRUE
    /\ UNCHANGED <<phase, sourceDigest, claimHeld, snapshotExists,
                    snapshotIndependent, written, checkpoint,
                    tailAuthentic, footer, published, tampered,
                    cancelRequested, retainPartial, partialRetained,
                    daemonUp, crashCount,
                    snapshotEffects, sealEffects>>

AcquireClaim ==
    /\ daemonUp
    /\ phase = "draft"
    /\ sourceStopped
    /\ phase' = "claimed"
    /\ claimHeld' = TRUE
    /\ UNCHANGED <<sourceDigest, sourceStopped, snapshotExists,
                    snapshotIndependent, written, checkpoint,
                    tailAuthentic, footer, published, tampered,
                    cancelRequested, retainPartial, partialRetained,
                    daemonUp, crashCount,
                    snapshotEffects, sealEffects>>

\* This atomic step represents the provider's durable, source-independent COW
\* snapshot proof. The source claim is released only in the same proved step.
CreateSnapshot ==
    /\ daemonUp
    /\ phase = "claimed"
    /\ claimHeld
    /\ snapshotEffects = 0
    /\ phase' = "snapshotted"
    /\ claimHeld' = FALSE
    /\ snapshotExists' = TRUE
    /\ snapshotIndependent' = TRUE
    /\ snapshotEffects' = snapshotEffects + 1
    /\ UNCHANGED <<sourceDigest, sourceStopped, written, checkpoint,
                    tailAuthentic, footer, published, tampered,
                    cancelRequested, retainPartial, partialRetained,
                    daemonUp, crashCount, sealEffects>>

BeginWriting ==
    /\ daemonUp
    /\ phase = "snapshotted"
    /\ snapshotExists
    /\ snapshotIndependent
    /\ phase' = "writing"
    /\ UNCHANGED <<sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    checkpoint, tailAuthentic, footer, published,
                    tampered, cancelRequested, retainPartial,
                    partialRetained, daemonUp, crashCount,
                    snapshotEffects, sealEffects>>

WriteNextChunk ==
    /\ daemonUp
    /\ phase = "writing"
    /\ tailAuthentic
    /\ written < MaxChunks
    /\ written' = written + 1
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, checkpoint,
                    tailAuthentic, footer, published, tampered,
                    cancelRequested, retainPartial, partialRetained,
                    daemonUp, crashCount,
                    snapshotEffects, sealEffects>>

CheckpointPrefix ==
    /\ daemonUp
    /\ phase = "writing"
    /\ tailAuthentic
    /\ checkpoint < written
    /\ checkpoint' = written
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    tailAuthentic, footer, published, tampered,
                    cancelRequested, retainPartial, partialRetained,
                    daemonUp, crashCount,
                    snapshotEffects, sealEffects>>

\* A crash can tear only the uncheckpointed suffix. Durable source snapshots,
\* authenticated checkpoints, and completed records survive.
Crash ==
    /\ daemonUp
    /\ phase \notin TerminalPhases
    /\ crashCount < MaxCrashes
    /\ daemonUp' = FALSE
    /\ crashCount' = crashCount + 1
    /\ tailAuthentic' =
          IF phase = "writing" /\ written > checkpoint
          THEN FALSE
          ELSE tailAuthentic
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    checkpoint, footer, published, tampered,
                    cancelRequested, retainPartial, partialRetained,
                    snapshotEffects, sealEffects>>

Restart ==
    /\ ~daemonUp
    /\ daemonUp' = TRUE
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    checkpoint, tailAuthentic, footer, published,
                    tampered, cancelRequested, retainPartial,
                    partialRetained, crashCount,
                    snapshotEffects, sealEffects>>

TruncateUnverifiedTail ==
    /\ daemonUp
    /\ phase = "writing"
    /\ ~tailAuthentic
    /\ written' = checkpoint
    /\ tailAuthentic' = TRUE
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, checkpoint,
                    footer, published, tampered, cancelRequested,
                    retainPartial, partialRetained,
                    daemonUp, crashCount, snapshotEffects, sealEffects>>

Seal ==
    /\ daemonUp
    /\ phase = "writing"
    /\ tailAuthentic
    /\ written = MaxChunks
    /\ checkpoint = MaxChunks
    /\ sealEffects = 0
    /\ phase' = "sealed"
    /\ footer' = TRUE
    /\ published' = TRUE
    /\ sealEffects' = sealEffects + 1
    /\ UNCHANGED <<sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    checkpoint, tailAuthentic, tampered,
                    cancelRequested, retainPartial, partialRetained,
                    daemonUp, crashCount,
                    snapshotEffects>>

RequestCancel ==
    /\ daemonUp
    /\ phase \in {"claimed", "snapshotted", "writing"}
    /\ ~cancelRequested
    /\ cancelRequested' = TRUE
    /\ retainPartial' \in BOOLEAN
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    checkpoint, tailAuthentic, footer, published,
                    tampered, partialRetained, daemonUp, crashCount,
                    snapshotEffects,
                    sealEffects>>

Cancel ==
    /\ daemonUp
    /\ phase \in {"claimed", "snapshotted", "writing"}
    /\ cancelRequested
    /\ phase' = "cancelled"
    /\ claimHeld' = FALSE
    /\ snapshotExists' = FALSE
    /\ snapshotIndependent' = FALSE
    /\ partialRetained' = (retainPartial /\ written > 0)
    /\ written' = 0
    /\ checkpoint' = 0
    /\ tailAuthentic' = TRUE
    /\ footer' = FALSE
    /\ published' = FALSE
    /\ UNCHANGED <<sourceDigest, sourceStopped, tampered,
                    cancelRequested, retainPartial, daemonUp, crashCount,
                    snapshotEffects, sealEffects>>

RemoveRetainedPartial ==
    /\ daemonUp
    /\ phase = "cancelled"
    /\ partialRetained
    /\ partialRetained' = FALSE
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    checkpoint, tailAuthentic, footer, published,
                    tampered, cancelRequested, retainPartial, daemonUp,
                    crashCount, snapshotEffects, sealEffects>>

\* External alteration is modeled only to prove that authenticated import
\* rejects it. Hideout itself has no transition that rewrites a sealed bundle.
Tamper ==
    /\ phase = "sealed"
    /\ ~tampered
    /\ tampered' = TRUE
    /\ UNCHANGED <<phase, sourceDigest, sourceStopped, claimHeld,
                    snapshotExists, snapshotIndependent, written,
                    checkpoint, tailAuthentic, footer, published,
                    cancelRequested, retainPartial, partialRetained,
                    daemonUp, crashCount,
                    snapshotEffects, sealEffects>>

Idle == UNCHANGED vars

Next ==
    \/ StopSource
    \/ AcquireClaim
    \/ CreateSnapshot
    \/ BeginWriting
    \/ WriteNextChunk
    \/ CheckpointPrefix
    \/ Crash
    \/ Restart
    \/ TruncateUnverifiedTail
    \/ Seal
    \/ RequestCancel
    \/ Cancel
    \/ RemoveRetainedPartial
    \/ Tamper
    \/ Idle

ProgressFairness ==
    /\ WF_vars(StopSource)
    /\ WF_vars(AcquireClaim)
    /\ WF_vars(CreateSnapshot)
    /\ WF_vars(BeginWriting)
    /\ WF_vars(WriteNextChunk)
    /\ WF_vars(CheckpointPrefix)
    /\ WF_vars(Restart)
    /\ WF_vars(TruncateUnverifiedTail)
    /\ WF_vars(Seal)
    /\ WF_vars(Cancel)

SafetySpec == Init /\ [][Next]_vars
Spec == Init /\ [][Next]_vars /\ ProgressFairness

TypeOK ==
    /\ phase \in Phases
    /\ sourceDigest = SourceDigest
    /\ sourceStopped \in BOOLEAN
    /\ claimHeld \in BOOLEAN
    /\ snapshotExists \in BOOLEAN
    /\ snapshotIndependent \in BOOLEAN
    /\ written \in 0..MaxChunks
    /\ checkpoint \in 0..MaxChunks
    /\ tailAuthentic \in BOOLEAN
    /\ footer \in BOOLEAN
    /\ published \in BOOLEAN
    /\ tampered \in BOOLEAN
    /\ cancelRequested \in BOOLEAN
    /\ retainPartial \in BOOLEAN
    /\ partialRetained \in BOOLEAN
    /\ daemonUp \in BOOLEAN
    /\ crashCount \in 0..MaxCrashes
    /\ snapshotEffects \in 0..1
    /\ sealEffects \in 0..1

SourceContentUnchanged == sourceDigest = SourceDigest

SourceClaimSafety ==
    claimHeld =>
        phase = "claimed" /\ sourceStopped /\ ~snapshotIndependent

SnapshotBeforePayload ==
    (written > 0 \/ checkpoint > 0 \/ footer \/ published) =>
        snapshotExists /\ snapshotIndependent

AuthenticatedCheckpointPrefix ==
    /\ checkpoint <= written
    /\ (~tailAuthentic =>
          phase = "writing" /\ written > checkpoint)

SealedBundleComplete ==
    phase = "sealed" =>
        /\ snapshotExists
        /\ snapshotIndependent
        /\ ~claimHeld
        /\ written = MaxChunks
        /\ checkpoint = MaxChunks
        /\ tailAuthentic
        /\ footer
        /\ published
        /\ sealEffects = 1

PublishedOnlySealed == published => phase = "sealed" /\ footer

CancelledNeverPublished ==
    phase = "cancelled" => ~published /\ ~footer /\ ~claimHeld

RetainedPartialNeverImportable == partialRetained => ~Importable

TamperedNeverImportable == tampered => ~Importable

CriticalEffectsAtMostOnce ==
    /\ snapshotEffects <= 1
    /\ sealEffects <= 1

ExportEventuallyTerminal ==
    (phase = "draft") ~> (phase \in TerminalPhases)

WritingEventuallyTerminal ==
    (phase = "writing") ~> (phase \in TerminalPhases)

ClaimEventuallyReleased == claimHeld ~> ~claimHeld

CrashEventuallyRestarts == (~daemonUp) ~> daemonUp

=============================================================================
