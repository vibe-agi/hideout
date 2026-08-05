------------------------ MODULE MigrationAdoption ------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS Destinations, Names, ControlIDs, BackendIDs, GuestIDs,
          SourceControlID, SourceBackendID, SourceGuestID,
          BundleDigest, MaxCrashes

ASSUME Destinations # {}
ASSUME Names # {}
ASSUME Cardinality(ControlIDs) >= Cardinality(Destinations)
ASSUME Cardinality(BackendIDs) >= Cardinality(Destinations)
ASSUME Cardinality(GuestIDs) >= Cardinality(Destinations)
ASSUME SourceControlID \notin ControlIDs
ASSUME SourceBackendID \notin BackendIDs
ASSUME SourceGuestID \notin GuestIDs
ASSUME MaxCrashes \in Nat

VARIABLES phase,
          requestedName,
          policy,
          plannedPolicy,
          bundleDigest,
          bundleValid,
          nameOwner,
          controlID,
          backendID,
          guestID,
          authorityApproved,
          authorityEffective,
          staged,
          profileStateStaged,
          receiptValid,
          runnable,
          profileVisible,
          profileStateVisible,
          environmentVisible,
          decision,
          daemonUp,
          crashCount,
          stageEffects,
          adoptionEffects,
          commitEffects,
          replacementEffects

vars ==
    <<phase, requestedName, policy, plannedPolicy, bundleDigest,
      bundleValid, nameOwner, controlID, backendID, guestID,
      authorityApproved, authorityEffective, staged, profileStateStaged,
      receiptValid, runnable, profileVisible, profileStateVisible,
      environmentVisible, decision, daemonUp,
      crashCount, stageEffects,
      adoptionEffects, commitEffects, replacementEffects>>

NoID == "none"
NoOwner == "none"
NoPolicy == "unset"
ExternalOwner == "external-destination"

Policies == {"safe-clone", "exact-guest-restore"}
Phases == {"draft", "planned", "claimed", "staged", "adopting",
           "adopted", "verified", "committed", "active",
           "rolling-back", "rolled-back", "blocked",
           "replacement-planned", "replacement-confirmed"}
TerminalPhases == {"active", "rolled-back", "blocked"}
ClaimPhases == {"claimed", "staged", "adopting", "adopted", "verified",
                "committed", "active", "rolling-back"}
IdentityPhases == {"adopted", "verified", "committed", "active"}

UsedControlIDs ==
    {controlID[destination] : destination \in Destinations} \ {NoID}
UsedBackendIDs ==
    {backendID[destination] : destination \in Destinations} \ {NoID}
UsedSafeGuestIDs ==
    {guestID[destination] : destination \in Destinations} \ {NoID,
                                                               SourceGuestID}

HasNameClaim(destination) ==
    nameOwner[requestedName[destination]] = destination

ProfileStateVars == <<profileStateStaged, profileStateVisible>>

Init ==
    /\ phase = [destination \in Destinations |-> "draft"]
    /\ requestedName \in [Destinations -> Names]
    /\ policy \in [Destinations -> Policies]
    /\ plannedPolicy = [destination \in Destinations |-> NoPolicy]
    /\ bundleDigest = BundleDigest
    /\ bundleValid \in BOOLEAN
    /\ nameOwner \in [Names -> {NoOwner, ExternalOwner}]
    /\ controlID = [destination \in Destinations |-> NoID]
    /\ backendID = [destination \in Destinations |-> NoID]
    /\ guestID = [destination \in Destinations |-> NoID]
    /\ authorityApproved = [destination \in Destinations |-> FALSE]
    /\ authorityEffective = [destination \in Destinations |-> FALSE]
    /\ staged = [destination \in Destinations |-> FALSE]
    /\ profileStateStaged = [destination \in Destinations |-> FALSE]
    /\ receiptValid = [destination \in Destinations |-> FALSE]
    /\ runnable = [destination \in Destinations |-> FALSE]
    /\ profileVisible = [destination \in Destinations |-> FALSE]
    /\ profileStateVisible = [destination \in Destinations |-> FALSE]
    /\ environmentVisible = [destination \in Destinations |-> FALSE]
    /\ decision = [destination \in Destinations |-> "none"]
    /\ daemonUp = TRUE
    /\ crashCount = 0
    /\ stageEffects = [destination \in Destinations |-> 0]
    /\ adoptionEffects = [destination \in Destinations |-> 0]
    /\ commitEffects = [destination \in Destinations |-> 0]
    /\ replacementEffects = [destination \in Destinations |-> 0]

RejectInvalidBundle(destination) ==
    /\ daemonUp
    /\ phase[destination] = "draft"
    /\ ~bundleValid
    /\ phase' = [phase EXCEPT ![destination] = "blocked"]
	/\ UNCHANGED ProfileStateVars
	/\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects,
                    replacementEffects>>

PlanDestination(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "draft"
    /\ \E freshControl \in ControlIDs \ UsedControlIDs,
          freshBackend \in BackendIDs \ UsedBackendIDs :
        /\ phase' = [phase EXCEPT ![destination] = "planned"]
        /\ plannedPolicy' =
              [plannedPolicy EXCEPT ![destination] = policy[destination]]
        /\ controlID' =
              [controlID EXCEPT ![destination] = freshControl]
        /\ backendID' =
              [backendID EXCEPT ![destination] = freshBackend]
        /\ UNCHANGED ProfileStateVars
        /\ UNCHANGED <<requestedName, policy, bundleDigest, bundleValid,
                        nameOwner, guestID, authorityApproved,
                        authorityEffective, staged, receiptValid, runnable,
                        profileVisible, environmentVisible,
                        decision, daemonUp, crashCount, stageEffects,
                        adoptionEffects, commitEffects, replacementEffects>>

ApproveAuthority(destination) ==
    /\ daemonUp
    /\ phase[destination] = "planned"
    /\ ~authorityApproved[destination]
    /\ authorityApproved' =
          [authorityApproved EXCEPT ![destination] = TRUE]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<phase, requestedName, policy, plannedPolicy,
                    bundleDigest, bundleValid, nameOwner, controlID,
                    backendID, guestID, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects,
                    replacementEffects>>

AcquireNameClaim(destination) ==
    /\ daemonUp
    /\ phase[destination] = "planned"
    /\ nameOwner[requestedName[destination]] = NoOwner
    /\ phase' = [phase EXCEPT ![destination] = "claimed"]
    /\ nameOwner' =
          [nameOwner EXCEPT ![requestedName[destination]] = destination]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects,
                    replacementEffects>>

BlockNameConflict(destination) ==
    /\ daemonUp
    /\ phase[destination] = "planned"
    /\ nameOwner[requestedName[destination]] # NoOwner
    /\ nameOwner[requestedName[destination]] # destination
    /\ phase' = [phase EXCEPT ![destination] = "blocked"]
    /\ controlID' = [controlID EXCEPT ![destination] = NoID]
    /\ backendID' = [backendID EXCEPT ![destination] = NoID]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, guestID, authorityApproved,
                    authorityEffective, staged, receiptValid, runnable,
                    profileVisible, environmentVisible,
                    decision, daemonUp, crashCount, stageEffects,
                    adoptionEffects, commitEffects, replacementEffects>>

RenameDestination(destination) ==
    /\ daemonUp
    /\ phase[destination] = "planned"
    /\ nameOwner[requestedName[destination]] # NoOwner
    /\ nameOwner[requestedName[destination]] # destination
    /\ \E replacementName \in Names :
        /\ replacementName # requestedName[destination]
        /\ nameOwner[replacementName] = NoOwner
        /\ phase' = [phase EXCEPT ![destination] = "draft"]
        /\ requestedName' =
              [requestedName EXCEPT ![destination] = replacementName]
        /\ plannedPolicy' =
              [plannedPolicy EXCEPT ![destination] = NoPolicy]
        /\ controlID' = [controlID EXCEPT ![destination] = NoID]
        /\ backendID' = [backendID EXCEPT ![destination] = NoID]
        /\ UNCHANGED ProfileStateVars
        /\ UNCHANGED <<policy, bundleDigest, bundleValid, nameOwner,
                        guestID, authorityApproved, authorityEffective,
                        staged, receiptValid, runnable, profileVisible,
                        environmentVisible, decision, daemonUp, crashCount,
                        stageEffects, adoptionEffects, commitEffects,
                        replacementEffects>>

PlanReplacement(destination) ==
    /\ daemonUp
    /\ phase[destination] = "planned"
    /\ nameOwner[requestedName[destination]] = ExternalOwner
    /\ phase' = [phase EXCEPT ![destination] = "replacement-planned"]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects,
                    replacementEffects>>

ConfirmReplacement(destination) ==
    /\ daemonUp
    /\ phase[destination] = "replacement-planned"
    /\ nameOwner[requestedName[destination]] = ExternalOwner
    /\ phase' = [phase EXCEPT ![destination] = "replacement-confirmed"]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects,
                    replacementEffects>>

DeleteReplacement(destination) ==
    /\ daemonUp
    /\ phase[destination] = "replacement-confirmed"
    /\ nameOwner[requestedName[destination]] = ExternalOwner
    /\ replacementEffects[destination] = 0
    /\ phase' = [phase EXCEPT ![destination] = "draft"]
    /\ plannedPolicy' =
          [plannedPolicy EXCEPT ![destination] = NoPolicy]
    /\ nameOwner' =
          [nameOwner EXCEPT ![requestedName[destination]] = NoOwner]
    /\ controlID' = [controlID EXCEPT ![destination] = NoID]
    /\ backendID' = [backendID EXCEPT ![destination] = NoID]
    /\ replacementEffects' =
          [replacementEffects EXCEPT ![destination] = @ + 1]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, bundleDigest, bundleValid,
                    guestID, authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects>>

StageDestination(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "claimed"
    /\ HasNameClaim(destination)
    /\ stageEffects[destination] = 0
    /\ phase' = [phase EXCEPT ![destination] = "staged"]
    /\ staged' = [staged EXCEPT ![destination] = TRUE]
    /\ profileStateStaged' =
          [profileStateStaged EXCEPT ![destination] = TRUE]
    /\ stageEffects' = [stageEffects EXCEPT ![destination] = @ + 1]
    /\ UNCHANGED profileStateVisible
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, receiptValid,
                    runnable, profileVisible, environmentVisible, decision,
                    daemonUp, crashCount,
                    adoptionEffects, commitEffects, replacementEffects>>

BeginAdoption(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "staged"
    /\ staged[destination]
    /\ phase' = [phase EXCEPT ![destination] = "adopting"]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects,
                    replacementEffects>>

FinishSafeClone(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "adopting"
    /\ plannedPolicy[destination] = "safe-clone"
    /\ adoptionEffects[destination] = 0
    /\ \E freshGuest \in GuestIDs \ UsedSafeGuestIDs :
        /\ phase' = [phase EXCEPT ![destination] = "adopted"]
        /\ guestID' = [guestID EXCEPT ![destination] = freshGuest]
        /\ receiptValid' =
              [receiptValid EXCEPT ![destination] = TRUE]
        /\ adoptionEffects' =
              [adoptionEffects EXCEPT ![destination] = @ + 1]
        /\ UNCHANGED ProfileStateVars
        /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                        bundleValid, nameOwner, controlID, backendID,
                        authorityApproved, authorityEffective, staged,
                        runnable, profileVisible, environmentVisible, decision,
                        daemonUp, crashCount,
                        stageEffects, commitEffects, replacementEffects>>

FinishExactRestore(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "adopting"
    /\ plannedPolicy[destination] = "exact-guest-restore"
    /\ adoptionEffects[destination] = 0
    /\ phase' = [phase EXCEPT ![destination] = "adopted"]
    /\ guestID' = [guestID EXCEPT ![destination] = SourceGuestID]
    /\ receiptValid' = [receiptValid EXCEPT ![destination] = TRUE]
    /\ adoptionEffects' =
          [adoptionEffects EXCEPT ![destination] = @ + 1]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID,
                    authorityApproved, authorityEffective, staged,
                    runnable, profileVisible, environmentVisible, decision,
                    daemonUp, crashCount,
                    stageEffects, commitEffects, replacementEffects>>

VerifyDestination(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "adopted"
    /\ staged[destination]
    /\ receiptValid[destination]
    /\ phase' = [phase EXCEPT ![destination] = "verified"]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, decision, daemonUp, crashCount,
                    stageEffects, adoptionEffects, commitEffects,
                    replacementEffects>>

DecideCommit(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "verified"
    /\ decision[destination] = "none"
    /\ commitEffects[destination] = 0
    /\ phase' = [phase EXCEPT ![destination] = "committed"]
    /\ decision' = [decision EXCEPT ![destination] = "commit"]
    /\ commitEffects' = [commitEffects EXCEPT ![destination] = @ + 1]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, daemonUp, crashCount,
	                stageEffects, adoptionEffects, replacementEffects>>

Activate(destination) ==
    /\ daemonUp
    /\ bundleValid
    /\ phase[destination] = "committed"
    /\ decision[destination] = "commit"
    /\ phase' = [phase EXCEPT ![destination] = "active"]
    /\ runnable' = [runnable EXCEPT ![destination] = TRUE]
    /\ profileVisible' =
          [profileVisible EXCEPT ![destination] = TRUE]
    /\ profileStateVisible' =
          [profileStateVisible EXCEPT ![destination] = TRUE]
    /\ environmentVisible' =
          [environmentVisible EXCEPT ![destination] = TRUE]
    /\ authorityEffective' =
          [authorityEffective EXCEPT
              ![destination] = authorityApproved[destination]]
    /\ UNCHANGED profileStateStaged
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, staged, receiptValid, decision,
                    daemonUp, crashCount, stageEffects, adoptionEffects,
	                commitEffects, replacementEffects>>

RequestRollback(destination) ==
    /\ daemonUp
    /\ phase[destination] \in
          {"claimed", "staged", "adopting", "adopted", "verified"}
    /\ decision[destination] = "none"
    /\ phase' = [phase EXCEPT ![destination] = "rolling-back"]
    /\ decision' = [decision EXCEPT ![destination] = "rollback"]
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, nameOwner, controlID, backendID, guestID,
                    authorityApproved, authorityEffective, staged,
                    receiptValid, runnable, profileVisible,
                    environmentVisible, daemonUp, crashCount,
	                stageEffects, adoptionEffects, commitEffects,
	                replacementEffects>>

Rollback(destination) ==
    /\ daemonUp
    /\ phase[destination] = "rolling-back"
    /\ decision[destination] = "rollback"
    /\ HasNameClaim(destination)
    /\ phase' = [phase EXCEPT ![destination] = "rolled-back"]
    /\ nameOwner' =
          [nameOwner EXCEPT ![requestedName[destination]] = NoOwner]
    /\ controlID' = [controlID EXCEPT ![destination] = NoID]
    /\ backendID' = [backendID EXCEPT ![destination] = NoID]
    /\ guestID' = [guestID EXCEPT ![destination] = NoID]
    /\ authorityEffective' =
          [authorityEffective EXCEPT ![destination] = FALSE]
    /\ staged' = [staged EXCEPT ![destination] = FALSE]
    /\ profileStateStaged' =
          [profileStateStaged EXCEPT ![destination] = FALSE]
    /\ receiptValid' = [receiptValid EXCEPT ![destination] = FALSE]
    /\ runnable' = [runnable EXCEPT ![destination] = FALSE]
    /\ profileVisible' =
          [profileVisible EXCEPT ![destination] = FALSE]
    /\ profileStateVisible' =
          [profileStateVisible EXCEPT ![destination] = FALSE]
    /\ environmentVisible' =
          [environmentVisible EXCEPT ![destination] = FALSE]
    /\ UNCHANGED <<requestedName, policy, plannedPolicy, bundleDigest,
                    bundleValid, authorityApproved, decision, daemonUp,
                    crashCount, stageEffects, adoptionEffects,
	                commitEffects, replacementEffects>>

Crash ==
    /\ daemonUp
    /\ crashCount < MaxCrashes
    /\ daemonUp' = FALSE
    /\ crashCount' = crashCount + 1
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<phase, requestedName, policy, plannedPolicy,
                    bundleDigest, bundleValid, nameOwner, controlID,
                    backendID, guestID, authorityApproved,
                    authorityEffective, staged, receiptValid, runnable,
                    profileVisible, environmentVisible,
                    decision, stageEffects, adoptionEffects,
	                commitEffects, replacementEffects>>

Restart ==
    /\ ~daemonUp
    /\ daemonUp' = TRUE
    /\ UNCHANGED ProfileStateVars
    /\ UNCHANGED <<phase, requestedName, policy, plannedPolicy,
                    bundleDigest, bundleValid, nameOwner, controlID,
                    backendID, guestID, authorityApproved,
                    authorityEffective, staged, receiptValid, runnable,
                    profileVisible, environmentVisible,
                    decision, crashCount, stageEffects, adoptionEffects,
	                commitEffects, replacementEffects>>

Idle == UNCHANGED vars

Next ==
    \/ \E destination \in Destinations : RejectInvalidBundle(destination)
    \/ \E destination \in Destinations : PlanDestination(destination)
    \/ \E destination \in Destinations : ApproveAuthority(destination)
    \/ \E destination \in Destinations : AcquireNameClaim(destination)
    \/ \E destination \in Destinations : BlockNameConflict(destination)
    \/ \E destination \in Destinations : RenameDestination(destination)
    \/ \E destination \in Destinations : PlanReplacement(destination)
    \/ \E destination \in Destinations : ConfirmReplacement(destination)
    \/ \E destination \in Destinations : DeleteReplacement(destination)
    \/ \E destination \in Destinations : StageDestination(destination)
    \/ \E destination \in Destinations : BeginAdoption(destination)
    \/ \E destination \in Destinations : FinishSafeClone(destination)
    \/ \E destination \in Destinations : FinishExactRestore(destination)
    \/ \E destination \in Destinations : VerifyDestination(destination)
    \/ \E destination \in Destinations : DecideCommit(destination)
    \/ \E destination \in Destinations : Activate(destination)
    \/ \E destination \in Destinations : RequestRollback(destination)
    \/ \E destination \in Destinations : Rollback(destination)
    \/ Crash
    \/ Restart
    \/ Idle

ProgressFairness ==
    /\ WF_vars(Restart)
    /\ \A destination \in Destinations :
          WF_vars(RejectInvalidBundle(destination))
    /\ \A destination \in Destinations :
          WF_vars(PlanDestination(destination))
    /\ \A destination \in Destinations :
          WF_vars(AcquireNameClaim(destination))
    /\ \A destination \in Destinations :
          WF_vars(BlockNameConflict(destination))
    /\ \A destination \in Destinations :
          WF_vars(RenameDestination(destination))
    /\ \A destination \in Destinations :
          WF_vars(PlanReplacement(destination))
    /\ \A destination \in Destinations :
          WF_vars(ConfirmReplacement(destination))
    /\ \A destination \in Destinations :
          WF_vars(DeleteReplacement(destination))
    /\ \A destination \in Destinations :
          WF_vars(StageDestination(destination))
    /\ \A destination \in Destinations :
          WF_vars(BeginAdoption(destination))
    /\ \A destination \in Destinations :
          WF_vars(FinishSafeClone(destination))
    /\ \A destination \in Destinations :
          WF_vars(FinishExactRestore(destination))
    /\ \A destination \in Destinations :
          WF_vars(VerifyDestination(destination))
    /\ \A destination \in Destinations :
          WF_vars(DecideCommit(destination))
    /\ \A destination \in Destinations : WF_vars(Activate(destination))
    /\ \A destination \in Destinations : WF_vars(Rollback(destination))

SafetySpec == Init /\ [][Next]_vars
Spec == Init /\ [][Next]_vars /\ ProgressFairness

TypeOK ==
    /\ phase \in [Destinations -> Phases]
    /\ requestedName \in [Destinations -> Names]
    /\ policy \in [Destinations -> Policies]
    /\ plannedPolicy \in [Destinations -> Policies \cup {NoPolicy}]
    /\ bundleDigest = BundleDigest
    /\ bundleValid \in BOOLEAN
    /\ nameOwner \in
          [Names -> Destinations \cup {NoOwner, ExternalOwner}]
    /\ controlID \in [Destinations -> ControlIDs \cup {NoID}]
    /\ backendID \in [Destinations -> BackendIDs \cup {NoID}]
    /\ guestID \in
          [Destinations -> GuestIDs \cup {SourceGuestID, NoID}]
    /\ authorityApproved \in [Destinations -> BOOLEAN]
    /\ authorityEffective \in [Destinations -> BOOLEAN]
    /\ staged \in [Destinations -> BOOLEAN]
    /\ profileStateStaged \in [Destinations -> BOOLEAN]
    /\ receiptValid \in [Destinations -> BOOLEAN]
    /\ runnable \in [Destinations -> BOOLEAN]
    /\ profileVisible \in [Destinations -> BOOLEAN]
    /\ profileStateVisible \in [Destinations -> BOOLEAN]
    /\ environmentVisible \in [Destinations -> BOOLEAN]
    /\ decision \in
          [Destinations -> {"none", "commit", "rollback"}]
    /\ daemonUp \in BOOLEAN
    /\ crashCount \in 0..MaxCrashes
    /\ stageEffects \in [Destinations -> 0..1]
    /\ adoptionEffects \in [Destinations -> 0..1]
    /\ commitEffects \in [Destinations -> 0..1]
    /\ replacementEffects \in [Destinations -> 0..1]

BundleImmutable == bundleDigest = BundleDigest

RunnableIffActive ==
    \A destination \in Destinations :
        runnable[destination] <=> phase[destination] = "active"

AtomicNamespaceVisibility ==
    \A destination \in Destinations :
        /\ profileStateVisible[destination] = profileVisible[destination]
        /\ profileVisible[destination] = environmentVisible[destination]
        /\ environmentVisible[destination] = runnable[destination]

ProfileStateOwnedByStage ==
    \A destination \in Destinations :
        profileStateStaged[destination] = staged[destination]

ControlIDsFreshAndDistinct ==
    /\ \A destination \in Destinations :
          controlID[destination] # NoID =>
              controlID[destination] \in ControlIDs /\
              controlID[destination] # SourceControlID
    /\ \A left, right \in Destinations :
          left # right /\
          controlID[left] # NoID /\ controlID[right] # NoID =>
              controlID[left] # controlID[right]

BackendIDsFreshAndDistinct ==
    /\ \A destination \in Destinations :
          backendID[destination] # NoID =>
              backendID[destination] \in BackendIDs /\
              backendID[destination] # SourceBackendID
    /\ \A left, right \in Destinations :
          left # right /\
          backendID[left] # NoID /\ backendID[right] # NoID =>
              backendID[left] # backendID[right]

SafeCloneIDsFreshAndDistinct ==
    /\ \A destination \in Destinations :
          phase[destination] \in IdentityPhases /\
          plannedPolicy[destination] = "safe-clone" =>
              guestID[destination] \in GuestIDs /\
              guestID[destination] # SourceGuestID
    /\ \A left, right \in Destinations :
          left # right /\
          phase[left] \in IdentityPhases /\
          phase[right] \in IdentityPhases /\
          plannedPolicy[left] = "safe-clone" /\
          plannedPolicy[right] = "safe-clone" =>
              guestID[left] # guestID[right]

ExactRestorePreserves ==
    \A destination \in Destinations :
        phase[destination] \in IdentityPhases /\
        plannedPolicy[destination] = "exact-guest-restore" =>
            guestID[destination] = SourceGuestID

AuthorityRequiresApproval ==
    \A destination \in Destinations :
        authorityEffective[destination] =>
            authorityApproved[destination] /\ runnable[destination]

InvalidBundleNeverStages ==
    ~bundleValid =>
        \A destination \in Destinations :
            /\ ~staged[destination]
            /\ ~profileStateStaged[destination]
            /\ ~profileStateVisible[destination]
            /\ ~runnable[destination]
            /\ phase[destination] \in {"draft", "blocked"}

PolicyFrozenAfterPlan ==
    \A destination \in Destinations :
        phase[destination] \in
            {"planned", "claimed", "staged", "adopting", "adopted",
             "verified", "committed", "active", "rolling-back",
             "rolled-back", "replacement-planned",
             "replacement-confirmed"} =>
                plannedPolicy[destination] = policy[destination]

ExclusiveNameClaims ==
    /\ \A name \in Names :
          nameOwner[name] \in Destinations =>
              /\ requestedName[nameOwner[name]] = name
              /\ phase[nameOwner[name]] \in ClaimPhases
    /\ \A destination \in Destinations :
          HasNameClaim(destination) <=>
              phase[destination] \in ClaimPhases

EffectsAtMostOnce ==
    \A destination \in Destinations :
        /\ stageEffects[destination] <= 1
        /\ adoptionEffects[destination] <= 1
        /\ commitEffects[destination] <= 1
        /\ replacementEffects[destination] <= 1

ReplacementRequiresFreshImportPlan ==
    \A destination \in Destinations :
        replacementEffects[destination] = 1 /\
        phase[destination] = "draft" =>
            /\ plannedPolicy[destination] = NoPolicy
            /\ controlID[destination] = NoID
            /\ backendID[destination] = NoID
            /\ nameOwner[requestedName[destination]] # ExternalOwner
            /\ ~staged[destination]
            /\ ~profileStateStaged[destination]
            /\ ~runnable[destination]

StagedStateNeverRuns ==
    \A destination \in Destinations :
        phase[destination] # "active" =>
            /\ ~runnable[destination]
            /\ ~profileVisible[destination]
            /\ ~profileStateVisible[destination]
            /\ ~environmentVisible[destination]

EveryDestinationEventuallyTerminal ==
    \A destination \in Destinations :
        (phase[destination] \notin TerminalPhases)
            ~> (phase[destination] \in TerminalPhases)

CommittedEventuallyActive ==
    \A destination \in Destinations :
        (phase[destination] = "committed")
            ~> (phase[destination] = "active")

RollbackEventuallyTerminal ==
    \A destination \in Destinations :
        (phase[destination] = "rolling-back")
            ~> (phase[destination] = "rolled-back")

ClaimEventuallySettles ==
    \A destination \in Destinations :
        HasNameClaim(destination)
            ~> (phase[destination] \in {"active", "rolled-back"})

CrashEventuallyRestarts == (~daemonUp) ~> daemonUp

=============================================================================
