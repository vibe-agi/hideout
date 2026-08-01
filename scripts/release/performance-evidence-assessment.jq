def sha256:
  type == "string" and test("^[a-f0-9]{64}$");

def nonnegative_number:
  type == "number" and . >= 0;

def nonnegative_integer:
  nonnegative_number and . == floor;

def nearest_rank($values; $rank):
  ($values | sort | .[$rank - 1]);

def rounded_percent_delta($baseline; $observed):
  (((($observed - $baseline) / $baseline) * 100000) | round) / 1000;

. as $summary |
($summary.metrics.referenceWorkload) as $reference |
($reference.methodology.samples) as $sample_count |
($reference.methodology.warmups) as $warmup_count |
($sample_count + $warmup_count) as $total_count |
($reference.baseline.samples) as $baseline_elapsed |
($reference.observed.samples) as $observed_elapsed |
($reference.elapsedOverhead.samples) as $reported_elapsed_overhead |
([
  range(0; $sample_count) as $index |
  rounded_percent_delta(
    $baseline_elapsed[$index];
    $observed_elapsed[$index]
  )
]) as $derived_elapsed_overhead |
($reference.resourceUsage.samples) as $resource_samples |
([$resource_samples[] | select(.recorded)]) as $recorded_resources |
([
  $recorded_resources[] |
  ((.baseline.userMs + .baseline.systemMs)) as $baseline_cpu |
  ((.observed.userMs + .observed.systemMs)) as $observed_cpu |
  rounded_percent_delta($baseline_cpu; $observed_cpu)
]) as $cpu_overhead |
([
  $recorded_resources[] |
  .observed.userMs - .baseline.userMs
]) as $user_delta_ms |
([
  $recorded_resources[] |
  .observed.systemMs - .baseline.systemMs
]) as $system_delta_ms |
([
  $recorded_resources[] |
  (.observed.userMs + .observed.systemMs) -
    (.baseline.userMs + .baseline.systemMs)
]) as $total_cpu_delta_ms |
([
  $recorded_resources[] |
  .observed.involuntaryContextSwitches -
    .baseline.involuntaryContextSwitches
]) as $involuntary_delta |
(
  $summary.hostDiagnostics.quietHostConfirmed == true and
  $summary.hostDiagnostics.policy ==
    "operator-confirmed-quiet-host-known-contention-invalidates-run" and
  $summary.hostDiagnostics.initialContentionAssessment.passed == true and
  $summary.hostDiagnostics.initialContentionAssessment.method ==
    "three-one-second-process-snapshots-two-hit-rejection" and
  $summary.hostDiagnostics.initialContentionAssessment.samples == 3 and
  $summary.hostDiagnostics.initialContentionAssessment.minimumHits == 2 and
  $summary.hostDiagnostics.initialContentionAssessment.genericCPUPercentThreshold == 50 and
  $summary.hostDiagnostics.initialContentionAssessment.virtualizationCPUPercentThreshold == 5 and
  $summary.hostDiagnostics.initialContentionAssessment.buildOrTestCPUPercentThreshold == 10 and
  $summary.hostDiagnostics.initialContentionAssessment.path ==
    "host-contention-preflight.txt" and
  ($summary.hostDiagnostics.initialContentionAssessment.sha256 | sha256) and
  $summary.hostDiagnostics.measurementContentionAssessment.passed == true and
  $summary.hostDiagnostics.measurementContentionAssessment.method ==
    "continuous-one-second-three-hit-classified-contention-rejection-generic-diagnostics" and
  $summary.hostDiagnostics.measurementContentionAssessment.samples >= 3 and
  $summary.hostDiagnostics.measurementContentionAssessment.rollingWindow == 3 and
  $summary.hostDiagnostics.measurementContentionAssessment.minimumHits == 3 and
  $summary.hostDiagnostics.measurementContentionAssessment.genericHighCPUPolicy ==
    "diagnostic-only" and
  $summary.hostDiagnostics.measurementContentionAssessment.genericCPUPercentThreshold == 50 and
  $summary.hostDiagnostics.measurementContentionAssessment.virtualizationCPUPercentThreshold == 5 and
  $summary.hostDiagnostics.measurementContentionAssessment.buildOrTestCPUPercentThreshold == 10 and
  $summary.hostDiagnostics.measurementContentionAssessment.path ==
    "host-contention-measurement.txt" and
  ($summary.hostDiagnostics.measurementContentionAssessment.sha256 | sha256) and
  ($summary.hostDiagnostics.snapshots | length) == 3 and
  [$summary.hostDiagnostics.snapshots[] | [.phase, .path]] == [
    ["start", "host-state-start.txt"],
    ["before-real-lima", "host-state-before-real-lima.txt"],
    ["after-real-lima", "host-state-after-real-lima.txt"]
  ] and
  all($summary.hostDiagnostics.snapshots[]; (.sha256 | sha256)) and
  $sample_count == 30 and
  ($warmup_count | nonnegative_integer) and
  $warmup_count >= 1 and
  ($baseline_elapsed | length) == $sample_count and
  ($observed_elapsed | length) == $sample_count and
  all($baseline_elapsed[]; type == "number" and . > 0) and
  all($observed_elapsed[]; type == "number" and . > 0) and
  $reported_elapsed_overhead == $derived_elapsed_overhead and
  $reference.elapsedOverhead.median ==
    nearest_rank($derived_elapsed_overhead; 15) and
  $reference.elapsedOverhead.threshold == 10 and
  $reference.elapsedOverhead.thresholdPassed == true and
  $reference.elapsedOverhead.confidence.level == 0.95 and
  $reference.elapsedOverhead.confidence.method ==
    "one-sided-exact-binomial-order-statistic" and
  $reference.elapsedOverhead.confidence.rank == 20 and
  $reference.elapsedOverhead.confidence.upperBound ==
    nearest_rank($derived_elapsed_overhead; 20) and
  $reference.elapsedOverhead.confidence.upperBound <= 10 and
  $reference.elapsedOverhead.confidence.thresholdPassed == true and
  $reference.resourceUsage.scope == "reference-workload-child-process" and
  $reference.resourceUsage.source == "getrusage(RUSAGE_CHILDREN)" and
  $reference.resourceUsage.cpuTimeUnit == "milliseconds" and
  $reference.resourceUsage.contextSwitchUnit == "count" and
  ($reference.resourceUsage.acceptanceFilter | type) == "boolean" and
  ($resource_samples | length) == $total_count and
  [$resource_samples[].sampleIndex] == [range(1; $total_count + 1)] and
  [$resource_samples[].recorded] == [
    range(1; $total_count + 1) | . > $warmup_count
  ] and
  ($recorded_resources | length) == $sample_count and
  all($resource_samples[];
    (.sampleIndex | nonnegative_integer) and
    (.recorded | type) == "boolean" and
    all([.baseline, .observed][];
      (.userMs | nonnegative_number) and
      (.systemMs | nonnegative_number) and
      ((.userMs + .systemMs) > 0) and
      (.voluntaryContextSwitches | nonnegative_integer) and
      (.involuntaryContextSwitches | nonnegative_integer))) and
  nearest_rank($cpu_overhead; 15) <= 10 and
  nearest_rank($cpu_overhead; 20) <= 10 and
  $reference.observationIntegrity.noReportedLoss == true and
  $summary.validation.referenceMedianUpperConfidenceBoundWithinTenPercent == true and
  $summary.validation.quietHostExplicitlyConfirmed == true and
  $summary.validation.initialHostContentionAssessmentPassed == true and
  $summary.validation.measurementHostContentionAssessmentPassed == true and
  $summary.validation.hostDiagnosticsRetained == true
) as $valid |
if $valid then
  {
    schema: "hideout.performance-evidence-assessment/v1",
    result: "passed",
    targetWorkloadCPU: {
      scope: $reference.resourceUsage.scope,
      source: $reference.resourceUsage.source,
      samples: $sample_count,
      unit: "percent",
      pairedOverhead: $cpu_overhead,
      median: nearest_rank($cpu_overhead; 15),
      threshold: 10,
      confidence: {
        level: 0.95,
        method: "one-sided-exact-binomial-order-statistic",
        rank: 20,
        upperBound: nearest_rank($cpu_overhead; 20),
        thresholdPassed: (nearest_rank($cpu_overhead; 20) <= 10)
      },
      medianUserDeltaMs: nearest_rank($user_delta_ms; 15),
      medianSystemDeltaMs: nearest_rank($system_delta_ms; 15),
      medianTotalDeltaMs: nearest_rank($total_cpu_delta_ms; 15),
      medianInvoluntaryContextSwitchDelta:
        nearest_rank($involuntary_delta; 15),
      producerAcceptanceFilter:
        $reference.resourceUsage.acceptanceFilter,
      independentlyAccepted: true
    },
    elapsedTime: {
      scope: "reference-workload-paired-wall-clock",
      samples: $sample_count,
      unit: "percent",
      pairedOverhead: $derived_elapsed_overhead,
      median: nearest_rank($derived_elapsed_overhead; 15),
      threshold: 10,
      confidence: {
        level: 0.95,
        method: "one-sided-exact-binomial-order-statistic",
        rank: 20,
        upperBound: nearest_rank($derived_elapsed_overhead; 20),
        thresholdPassed: (nearest_rank($derived_elapsed_overhead; 20) <= 10)
      }
    },
    hostContention: {
      role: "eligibility-and-invalidation-only",
      initialPassed:
        $summary.hostDiagnostics.initialContentionAssessment.passed,
      continuousPassed:
        $summary.hostDiagnostics.measurementContentionAssessment.passed,
      continuousSamples:
        $summary.hostDiagnostics.measurementContentionAssessment.samples
    },
    observationIntegrity: {
      noReportedLoss: $reference.observationIntegrity.noReportedLoss
    }
  }
else
  error("performance evidence assessment failed")
end
