------------------------ MODULE StopObservation ------------------------
EXTENDS Naturals

CONSTANTS MaxBoots, MaxFalse

VARIABLES actual,
          boot,
          stopPending,
          phase,
          confirmTarget,
          streakKind,
          streakLen,
          falseBudget,
          lastFalse

vars == <<actual, boot, stopPending, phase, confirmTarget, streakKind,
          streakLen, falseBudget, lastFalse>>

ActualStates == {"running", "stopped"}
Phases == {"initial", "confirm", "poll", "reported", "failed"}
TerminalKinds == {"stopped", "absent"}
NoKind == "none"

\* The protocol is bound to boot 0: the incarnation the stop request named.
\* ExternalRestart models any third party bringing the VM back up as a new
\* incarnation while the stop protocol is still observing.

Init ==
    /\ actual = "running"
    /\ boot = 0
    /\ stopPending = FALSE
    /\ phase = "initial"
    /\ confirmTarget = NoKind
    /\ streakKind = NoKind
    /\ streakLen = 0
    /\ falseBudget = MaxFalse
    /\ lastFalse = FALSE

\* ---- environment ----

\* Force stop is asynchronous: issued in one step, effective in another.
StopTakesEffect ==
    /\ stopPending
    /\ actual = "running"
    /\ actual' = "stopped"
    /\ stopPending' = FALSE
    /\ UNCHANGED <<boot, phase, confirmTarget, streakKind, streakLen,
                    falseBudget, lastFalse>>

ExternalRestart ==
    /\ boot < MaxBoots
    /\ actual' = "running"
    /\ boot' = boot + 1
    /\ stopPending' = FALSE
    /\ UNCHANGED <<phase, confirmTarget, streakKind, streakLen,
                    falseBudget, lastFalse>>

\* ---- samples ----

\* A false sample is a transient inventory anomaly: a terminal or unknown
\* reading while the VM actually runs. The single-anomaly assumption the
\* implementation relies on is encoded here: anomalies are bounded and never
\* consecutive, so two identical terminal readings in a row contain at least
\* one true reading. Dropping ~lastFalse from this guard lets TLC produce
\* the false-success trace the two-consecutive-observations rule defends
\* against.
CanFalse ==
    /\ falseBudget > 0
    /\ ~lastFalse
    /\ actual = "running"

TerminalSampleOK(kind, isFalse) ==
    IF isFalse
      THEN CanFalse /\ kind \in TerminalKinds
      ELSE actual = "stopped" /\ kind = "stopped"

SpendSample(isFalse) ==
    /\ lastFalse' = isFalse
    /\ falseBudget' = IF isFalse THEN falseBudget - 1 ELSE falseBudget

\* ---- protocol: initial observation ----

InitialRunningBound ==
    /\ phase = "initial"
    /\ actual = "running"
    /\ boot = 0
    /\ stopPending' = TRUE
    /\ phase' = "poll"
    /\ SpendSample(FALSE)
    /\ UNCHANGED <<actual, boot, confirmTarget, streakKind, streakLen>>

InitialRunningForeign ==
    /\ phase = "initial"
    /\ actual = "running"
    /\ boot # 0
    /\ phase' = "failed"
    /\ SpendSample(FALSE)
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget, streakKind,
                    streakLen>>

\* An initially terminal reading is never accepted alone; it opens a
\* confirmation that must reproduce the same terminal kind.
InitialTerminal(kind, isFalse) ==
    /\ phase = "initial"
    /\ TerminalSampleOK(kind, isFalse)
    /\ confirmTarget' = kind
    /\ phase' = "confirm"
    /\ SpendSample(isFalse)
    /\ UNCHANGED <<actual, boot, stopPending, streakKind, streakLen>>

InitialUnknown ==
    /\ phase = "initial"
    /\ CanFalse
    /\ phase' = "failed"
    /\ SpendSample(TRUE)
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget, streakKind,
                    streakLen>>

\* ---- protocol: confirming an initially terminal reading ----

\* Only a reproduced stopped reading confirms success: stop keeps the
\* instance resumable, so a reproduced absence is a missing machine (or an
\* observer bound to the wrong backend world) and fails closed.
ConfirmMatches(kind, isFalse) ==
    /\ phase = "confirm"
    /\ TerminalSampleOK(kind, isFalse)
    /\ phase' = IF kind = confirmTarget /\ kind = "stopped"
                  THEN "reported"
                  ELSE "failed"
    /\ SpendSample(isFalse)
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget, streakKind,
                    streakLen>>

\* The confirmation that comes back running rebinds to that observation and
\* proceeds to stop it instead of trusting the earlier terminal sample.
ConfirmRunningBound ==
    /\ phase = "confirm"
    /\ actual = "running"
    /\ boot = 0
    /\ stopPending' = TRUE
    /\ phase' = "poll"
    /\ SpendSample(FALSE)
    /\ UNCHANGED <<actual, boot, confirmTarget, streakKind, streakLen>>

ConfirmRunningForeign ==
    /\ phase = "confirm"
    /\ actual = "running"
    /\ boot # 0
    /\ phase' = "failed"
    /\ SpendSample(FALSE)
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget, streakKind,
                    streakLen>>

ConfirmUnknown ==
    /\ phase = "confirm"
    /\ CanFalse
    /\ phase' = "failed"
    /\ SpendSample(TRUE)
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget, streakKind,
                    streakLen>>

\* ---- protocol: polling after the stop was issued ----

\* Terminal proof requires two consecutive identical terminal readings; a
\* different terminal kind restarts the streak rather than extending it. A
\* stable stopped reading is the only success: a stable absence means the
\* machine is gone or the observer is bound to the wrong backend world, and
\* the protocol fails closed instead of reporting a stop.
PollTerminal(kind, isFalse) ==
    LET streak == IF streakKind = kind THEN streakLen + 1 ELSE 1 IN
    /\ phase = "poll"
    /\ TerminalSampleOK(kind, isFalse)
    /\ streakKind' = kind
    /\ streakLen' = streak
    /\ phase' = IF streak < 2 THEN "poll"
                ELSE IF kind = "stopped" THEN "reported" ELSE "failed"
    /\ SpendSample(isFalse)
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget>>

PollRunningBound ==
    /\ phase = "poll"
    /\ actual = "running"
    /\ boot = 0
    /\ streakKind' = NoKind
    /\ streakLen' = 0
    /\ SpendSample(FALSE)
    /\ UNCHANGED <<actual, boot, stopPending, phase, confirmTarget>>

PollRunningForeign ==
    /\ phase = "poll"
    /\ actual = "running"
    /\ boot # 0
    /\ phase' = "failed"
    /\ SpendSample(FALSE)
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget, streakKind,
                    streakLen>>

PollUnknown ==
    /\ phase = "poll"
    /\ CanFalse
    /\ streakKind' = NoKind
    /\ streakLen' = 0
    /\ SpendSample(TRUE)
    /\ UNCHANGED <<actual, boot, stopPending, phase, confirmTarget>>

\* The proof window closing without stable terminal proof fails closed; it
\* never converts a partial streak into success.
WindowExpires ==
    /\ phase \in {"initial", "confirm", "poll"}
    /\ phase' = "failed"
    /\ UNCHANGED <<actual, boot, stopPending, confirmTarget, streakKind,
                    streakLen, falseBudget, lastFalse>>

Next ==
    \/ StopTakesEffect
    \/ ExternalRestart
    \/ InitialRunningBound
    \/ InitialRunningForeign
    \/ \E kind \in TerminalKinds, isFalse \in BOOLEAN :
           InitialTerminal(kind, isFalse)
    \/ InitialUnknown
    \/ \E kind \in TerminalKinds, isFalse \in BOOLEAN :
           ConfirmMatches(kind, isFalse)
    \/ ConfirmRunningBound
    \/ ConfirmRunningForeign
    \/ ConfirmUnknown
    \/ \E kind \in TerminalKinds, isFalse \in BOOLEAN :
           PollTerminal(kind, isFalse)
    \/ PollRunningBound
    \/ PollRunningForeign
    \/ PollUnknown
    \/ WindowExpires

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ actual \in ActualStates
    /\ boot \in 0..MaxBoots
    /\ stopPending \in BOOLEAN
    /\ phase \in Phases
    /\ confirmTarget \in TerminalKinds \cup {NoKind}
    /\ streakKind \in TerminalKinds \cup {NoKind}
    /\ streakLen \in 0..2
    /\ falseBudget \in 0..MaxFalse
    /\ lastFalse \in BOOLEAN

\* The false-stop property: success is never reported while the incarnation
\* the stop was bound to is still running. A later incarnation running is
\* outside this protocol's claim; the boot check fails it closed whenever it
\* is observed.
ReportedImpliesBoundIncarnationStopped ==
    phase = "reported" => ~(actual = "running" /\ boot = 0)

\* A pending force stop only ever exists for the bound incarnation; a
\* restart supersedes it rather than leaking into the new boot.
PendingStopIsBound ==
    stopPending => boot = 0

=============================================================================
