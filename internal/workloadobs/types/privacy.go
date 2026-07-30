package types

// These values are shared by every operator-facing disclosure. Keeping the
// language in the workload contract prevents doctor, support reports, and run
// boundary summaries from making different privacy or coverage claims.
const (
	ActivityObservationScope = "top-level-command-and-descendants"

	ActivityLocalPathVisibility    = "visible-in-authenticated-local-view"
	ActivityShareablePathTreatment = "excluded-from-shareable-support-review-before-export"

	ActivityCoverageNonClaim = "no-events-does-not-prove-no-behavior-without-Available-coverage-for-the-subsystem-and-window"

	ActivityRetentionOwner     = "exact-environment-or-disposable-session-plus-backend-incarnation"
	ActivityRetentionLifecycle = "clean-delete-recreate-removes-the-exact-old-owner"
)

// ActivityExcludedData returns data classes that the workload activity plane
// does not collect. The returned slice is a copy so callers cannot mutate the
// shared disclosure.
func ActivityExcludedData() []string {
	return []string{
		"file-content",
		"environment-values",
		"keystrokes",
		"full-pty",
		"packet-payload",
	}
}
