{
  schema: "hideout.release-evidence/v1",
  generatedAt: .generatedAt,
  result: "passed",
  stage: .stage,
  releaseReadiness: .releaseReadiness,
  source: {
    commit: .commit,
    tree: .tree,
    dirty: false,
    committedAt: .committedAt,
    manifest: .sourceManifest
  },
  candidate: {
    version: .version,
    tag: .tag,
    channel: "developer-preview",
    signingMode: "developer-preview-unsigned",
    publicationStatus: "local-only",
    archive: .archive,
    packageManifest: .packageManifest,
    packageSummary: .packageSummary,
    lifecycleSummary: .lifecycleSummary
  },
  package: {
    files: .files,
    helpers: .helpers,
    browserConsole: .browserConsole,
    runtime: .runtime
  },
  formal: .formal,
  gates: .gates,
  review: {
    result: "passed",
    requiredFindings: .reviewFindingCount,
    openRequiredFindings: 0,
    report: .review,
    claimMatrix: .claims
  },
  limitations: .limitations,
  closure: .closure,
  digest: {
    algorithm: "sha256",
    detachedPath: .detachedPath
  }
}
