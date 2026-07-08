#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

APP_TEST_NAMES=(
  ExplainInitializesProfileAndPrintsBoundary
  InitNoInputCreatesTemplateProfileAndFailsOnCollision
  DoctorFixDryRunDoesNotCreateProfile
  ExplainAndRunUseConfiguredIdentityEnv
  ExplainRequiresTargetCommandBeforeStateCreation
  ExplainEphemeralShowsSessionLocalIdentity
  ExplainUsesProfileCommandProxyConfig
  ExplainAliasPathModeUsesNeutralGuestWorkspace
  ExplainNativeTun2SocksShowsFailClosedAndHiddenSecret
  ExplainTun2SocksSecretErrorDoesNotExposeBackingEnvName
  RunNativeRequiresWeakIsolationFlag
  RunNativeMissingCommandReportsBackendContext
  ProfileCloneCommandCreatesPolicyClone
  ProfilePathRejectsInvalidProfileName
  ProfileInitRejectsExistingProfile
  CleanupCommandRemovesSessionEphemeralStateButKeepsAudit
  CleanupCommandDryRunKeepsFiles
  CleanupCommandSessionFilterKeepsOtherSessions
  CleanupAuditDetailsDoNotExposeRemovedPaths
  DoctorReportsCoreChecks
  DoctorRejectsGenericBrowserPath
  DoctorRejectsBrowserPathSymlinkToGenericOpener
  DoctorRejectsUnsupportedBrowserApp
  DoctorUsesAliasWorkspaceMapping
  DoctorEphemeralUsesSessionForkIdentity
  DoctorBadProxySecretFailsNetworkCheck
  DoctorMissingProxySecretDoesNotExposeBackingEnvName
  DoctorReportsMissingLima
  DoctorValidatesGeneratedLimaConfig
  DoctorReportsInvalidGeneratedLimaConfig
  DoctorReportsMissingLimaCommandProxyShim
  DoctorReportsBrokenLimaMount
  DoctorInvalidProfileReportsProfileError
  DoctorRequiresNetworkConnectCapability
  DoctorReportsPolicyScriptFailure
  DoctorRejectsCommandScriptProposalMismatchedToBrokerRequest
  DoctorPolicyScriptContextIncludesCommandTarget
  DoctorReportsAuditRedactionScriptFailure
  RunNativeExecutesWithWeakIsolationFlag
  RunNativeAcceptanceWorkspaceGitAndChildEnv
  RunAliasPathModeAuditsNeutralGuestWorkspace
  RunNativeEphemeralUsesSessionLocalIdentity
  RunNativeAuditRedactsCommandSecrets
  RunNativeOpenUsesBrokerShim
  RunNativeOpenRejectsHostLocalBrowserURL
  RunNativeOpenAllowsMappedWorkspaceFile
  RunNativeOpenRejectsUnmappedFile
  RunNativeOpenUsesUniqueBrokerRequestIDs
  RunNativeRejectsDisabledCommandProxyShim
  RunNativeOpenAuditsPolicyScriptParticipation
  Tun2SocksFailsClosedBeforeCommandRuns
  RunTun2SocksSecretErrorDoesNotExposeBackingEnvName
)
APP_TESTS="Test($(IFS='|'; echo "${APP_TEST_NAMES[*]}"))$"

go test -count=1 ./internal/app -run "$APP_TESTS"
go test -count=1 \
  ./internal/profile \
  ./internal/envpolicy \
  ./internal/secrets \
  ./internal/network \
  ./internal/session \
  ./internal/manager \
  ./internal/cmdproxy \
  ./internal/broker \
  ./internal/hostopen \
  ./internal/audit \
  ./internal/backend/native
