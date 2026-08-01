.schema == "hideout.release-evidence/v1" and
.result == "passed" and
.stage == $stage and
.releaseReadiness == $releaseReadiness and
.source.commit == $commit and
.source.tree == $tree and
.source.dirty == false and
.candidate.version == $version and
.candidate.archive.sha256 == $archiveSHA256 and
.candidate.publicationStatus == "local-only" and
(.package.files | length) >= 100 and
([.package.files[].path] | unique | length) ==
  (.package.files | length) and
all(.package.files[];
  if .executable then (.mode == "0700" or .mode == "0755")
  else (.mode == "0600" or .mode == "0644") end) and
(.package.helpers | length) == 13 and
([.package.helpers[] | select(.kind == "helper-manifest")] | length) == 6 and
([.package.helpers[] | select(.kind == "linux-helper")] | length) == 7 and
(.package.browserConsole.inventory.assets | length) == 8 and
.formal.configurationCount == 12 and
.formal.moduleCount == 10 and
.formal.invariantCount == 76 and
.formal.propertyCount == 19 and
.formal.goTestCount == 12 and
(.gates | length) == 11 and
([.gates[].id] | unique | length) == 11 and
all(.gates[]; .result == "passed") and
all(.gates[] | select(.scope == "candidate");
  .candidateAcceptance == true) and
.review.requiredFindings == $reviewFindingCount and
.review.openRequiredFindings == 0 and
(.limitations | length) >= 5 and
(
  if .stage == "final-ready" then
    .releaseReadiness == true and
    .closure.localInstall.status == "passed" and
    (.closure.localInstall.evidence | type) == "object" and
    .closure.publicationAbsence.status == "passed" and
    (.closure.publicationAbsence.evidence | type) == "object"
  else
    .releaseReadiness == false
  end
)
