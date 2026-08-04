package schemas_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const schemaBaseURL = "https://hideout.local/schemas/"

var feature045Schemas = []string{
	"activity-owner.schema.json",
	"activity-record.schema.json",
	"coverage-interval.schema.json",
	"daemon-event-v2.schema.json",
	"formal-inventory.schema.json",
	"local-install-candidate.schema.json",
	"migration-manifest.schema.json",
	"migration-operation-projection.schema.json",
	"migration-plan.schema.json",
	"migration-receipt.schema.json",
	"observer-frame.schema.json",
	"operation.schema.json",
	"operator-snapshot.schema.json",
	"profile-projection.schema.json",
	"publication-absence.schema.json",
	"release-evidence.schema.json",
}

func TestFeature045SchemasCompileWithRepositoryDependencies(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	paths, err := filepath.Glob("*.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no repository schemas found")
	}
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		document, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		if addErr := compiler.AddResource(schemaBaseURL+filepath.Base(path), document); addErr != nil {
			t.Fatalf("add %s: %v", path, addErr)
		}
	}
	for _, name := range feature045Schemas {
		t.Run(name, func(t *testing.T) {
			if _, compileErr := compiler.Compile(schemaBaseURL + name); compileErr != nil {
				t.Fatalf("compile %s: %v", name, compileErr)
			}
		})
	}
}

func TestHelperManifestSchemaBindsMigrationHelpersToTheirPlatform(t *testing.T) {
	schema := compileFeatureSchema(t, "helper-manifest.schema.json")
	fixture := func(command, targetOS string) map[string]any {
		return map[string]any{
			"version":    "hideout.helper-manifest/v1",
			"command":    command,
			"targetOS":   targetOS,
			"targetArch": "arm64",
			"artifact":   command + "-arm64",
			"sha256":     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"builder":    "go build -trimpath",
			"builtAt":    "2026-08-04T00:00:00Z",
		}
	}

	for _, test := range []struct {
		name     string
		command  string
		targetOS string
		valid    bool
	}{
		{name: "Linux migration adoption", command: "hideout-migration-adopt", targetOS: "linux", valid: true},
		{name: "Darwin VZ adoption", command: "hideout-migration-vz-adopt", targetOS: "darwin", valid: true},
		{name: "unknown command", command: "hideout-unknown-helper", targetOS: "linux", valid: false},
		{name: "Linux helper on Darwin", command: "hideout-migration-adopt", targetOS: "darwin", valid: false},
		{name: "VZ helper on Linux", command: "hideout-migration-vz-adopt", targetOS: "linux", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := schema.Validate(fixture(test.command, test.targetOS))
			if test.valid && err != nil {
				t.Fatalf("valid helper manifest: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("helper manifest accepted an invalid command/platform binding")
			}
		})
	}
}

func TestObserverFrameSchemaMatchesCurrentEnvelopeAndClosedKinds(t *testing.T) {
	schema := compileFeatureSchema(t, "observer-frame.schema.json")
	value := map[string]any{
		"schema":             "hideout.observation.v1",
		"owner":              reusableOwnerFixture(),
		"sessionId":          "ses_observer_fixture",
		"cgroupId":           uint64(3141),
		"observerGeneration": uint64(1),
		"cpu":                uint64(2),
		"sequence":           uint64(91),
		"monotonicNs":        uint64(88_200_011),
		"kind":               "process.execution",
		"payload": map[string]any{
			"schema": "hideout.execution.v1",
			"id":     "exec_observerfixture",
		},
	}
	for _, kind := range []string{
		"process.execution",
		"file.read",
		"file.hardlink",
		"file.symlink",
		"dns.response",
		"collector.heartbeat",
	} {
		value["kind"] = kind
		if err := schema.Validate(value); err != nil {
			t.Fatalf("valid observer kind %q: %v", kind, err)
		}
	}

	value["kind"] = "process.execution"
	value["collectorGeneration"] = uint64(1)
	if err := schema.Validate(value); err == nil {
		t.Fatal("observer frame accepted stale collectorGeneration field")
	}
	delete(value, "collectorGeneration")
	delete(value, "observerGeneration")
	if err := schema.Validate(value); err == nil {
		t.Fatal("observer frame accepted missing observerGeneration")
	}
	value["observerGeneration"] = uint64(1)

	value["cpu"] = uint64(4_294_967_295)
	if err := schema.Validate(value); err == nil {
		t.Fatal("observer transport CPU accepted a non-loss kind")
	}
	value["kind"] = "collector.loss"
	if err := schema.Validate(value); err != nil {
		t.Fatalf("observer transport loss frame: %v", err)
	}
}

func TestActivityOwnerSeparatesStableIncarnationFromGuestBoot(t *testing.T) {
	schema := compileFeatureSchema(t, "activity-owner.schema.json")
	valid := map[string]any{
		"kind":                 "reusable-environment",
		"environmentId":        "env_fixture",
		"backend":              "lima",
		"backendIncarnationId": "machine-fixture-1",
	}
	if err := schema.Validate(valid); err != nil {
		t.Fatalf("valid reusable owner: %v", err)
	}
	valid["guestBootId"] = "boot-must-not-be-owner"
	if err := schema.Validate(valid); err == nil {
		t.Fatal("activity owner accepted guest boot identity as retention identity")
	}
}

func TestCoverageRequiresGenerationLossAndRetentionFacts(t *testing.T) {
	schema := compileFeatureSchema(t, "coverage-interval.schema.json")
	value := map[string]any{
		"schema":    "hideout.coverage-interval.v1",
		"id":        "cov_fixture123",
		"owner":     reusableOwnerFixture(),
		"sessionId": "ses_fixture",
		"subsystem": "process",
		"state":     "Available",
		"reason":    "observer-ready",
		"startedAt": "2026-07-29T00:00:00Z",
	}
	if err := schema.Validate(value); err == nil {
		t.Fatal("coverage accepted a claim without generation/drop/retention facts")
	}
	value["collectorGeneration"] = 1
	value["droppedEventCount"] = 0
	value["retentionGap"] = false
	if err := schema.Validate(value); err != nil {
		t.Fatalf("valid coverage interval: %v", err)
	}
}

func TestActivityRecordSchemaAcceptsRepresentativeGoRecords(t *testing.T) {
	schema := compileFeatureSchema(t, "activity-record.schema.json")
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture",
		"lima",
		"machine-fixture-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	actor := &workloadtypes.Actor{
		ExecutionID: "exec_fixture123",
		PID:         42,
		UID:         1000,
		GID:         1000,
		User:        "fixture",
		Group:       "fixture",
	}
	fixtures := []struct {
		name    string
		kind    string
		subject any
	}{
		{
			name: "process",
			kind: workloadtypes.ActivityProcess,
			subject: workloadtypes.ProcessSubject{
				Kind:        workloadtypes.ActivityProcess,
				ExecutionID: actor.ExecutionID,
				Executable:  "/usr/bin/true",
				Argv:        []string{"true"},
				GuestIdentity: workloadtypes.GuestIdentity{
					UID: 1000,
					GID: 1000,
				},
			},
		},
		{
			name: "file",
			kind: workloadtypes.ActivityFile,
			subject: workloadtypes.FileSubject{
				Kind:        workloadtypes.ActivityFile,
				Path:        "/workspace/source",
				TargetPath:  "/workspace/target",
				PathState:   "resolved",
				PathClass:   "workspace",
				FileType:    "regular",
				Device:      8,
				Inode:       9,
				MountID:     10,
				Destructive: true,
			},
		},
		{
			name: "proxy domain",
			kind: workloadtypes.ActivityConnection,
			subject: workloadtypes.NetworkSubject{
				Kind:              workloadtypes.ActivityConnection,
				Protocol:          "tcp",
				IP:                "127.0.0.1",
				Port:              7890,
				TargetPort:        443,
				Domain:            "proxy.example.test",
				DomainAttribution: workloadtypes.AttributionExact,
				CorrelationReason: "validated-proxy-target",
				Route:             "proxy",
				Direction:         "egress",
				SocketCookie:      700,
			},
		},
		{
			name: "proxy IP",
			kind: workloadtypes.ActivityConnection,
			subject: workloadtypes.NetworkSubject{
				Kind:              workloadtypes.ActivityConnection,
				Protocol:          "tcp",
				IP:                "127.0.0.1",
				Port:              7890,
				TargetIP:          "2001:db8::70",
				TargetPort:        9443,
				DomainAttribution: workloadtypes.AttributionUnknown,
				CorrelationReason: "validated-proxy-ip-target",
				Route:             "proxy",
				Direction:         "egress",
				SocketCookie:      701,
			},
		},
		{
			name: "dns",
			kind: workloadtypes.ActivityDNS,
			subject: workloadtypes.DNSSubject{
				Kind:         workloadtypes.ActivityDNS,
				Query:        "example.test",
				QueryType:    "AAAA",
				Answers:      []string{"2001:db8::70"},
				TTLSeconds:   60,
				ResponseCode: "NOERROR",
				Resolver:     "1.1.1.1:53",
			},
		},
		{
			name: "risk",
			kind: workloadtypes.ActivityRisk,
			subject: workloadtypes.GenericSubject{
				Kind:    workloadtypes.ActivityRisk,
				Code:    "network.proxy",
				Summary: "validated proxy target",
			},
		},
	}
	for index, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			record := workloadtypes.ActivityRecord{
				Schema:          workloadtypes.ActivityRecordSchema,
				ID:              "act_fixture123",
				Owner:           owner,
				SessionID:       "ses_fixture",
				Actor:           actor,
				Kind:            fixture.kind,
				Operation:       "observe",
				Subject:         fixture.subject,
				Outcome:         workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
				Count:           1,
				FirstAt:         at,
				LastAt:          at,
				FirstSequence:   uint64(index + 1),
				LastSequence:    uint64(index + 1),
				Attribution:     workloadtypes.AttributionExact,
				CoverageID:      "cov_fixture123",
				RedactionStatus: workloadtypes.RedactionPassed,
			}
			if err := record.ValidatePersistable(); err != nil {
				t.Fatalf("Go validation: %v", err)
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); err != nil {
				t.Fatalf("JSON schema validation: %v\n%s", err, data)
			}
		})
	}
}

func TestDaemonEventV2SchemaAcceptsEveryProductionLegacyKind(t *testing.T) {
	schema := compileFeatureSchema(t, "daemon-event-v2.schema.json")
	for index, event := range liveconsole.RepresentativeEvents() {
		event.Version = liveconsole.EventVersionV2
		event.InstanceID = "daemon_schemafixture"
		event.CredentialGeneration = 4
		if event.Kind == liveconsole.KindTerminal {
			event.Seq = 0
		} else {
			event.Seq = index + 1
		}
		if err := liveconsole.ValidateEvent(event); err != nil {
			t.Fatalf("%s Go validation: %v", event.Kind, err)
		}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); err != nil {
			t.Fatalf("%s JSON schema validation: %v\n%s", event.Kind, err, data)
		}
	}
}

func TestReleaseClosureSchemasRejectFalseSuccessClaims(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	tree := "89abcdef0123456789abcdef0123456789abcdef"
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	artifact := map[string]any{
		"path":   "run-fixture/observation.json",
		"sha256": digest,
		"bytes":  42,
		"mode":   "0600",
	}
	localChecks := map[string]any{
		"sourceClean":                        true,
		"archiveDigestVerified":              true,
		"archiveSafetyVerified":              true,
		"packageVerified":                    true,
		"packageIdentityVerified":            true,
		"legacyInstallRemoved":               true,
		"legacyDataDiscarded":                true,
		"unrelatedHomebrewPreserved":         true,
		"setupCompleted":                     true,
		"daemonStarted":                      true,
		"secretStoredWithoutRetention":       true,
		"connectionPlanned":                  true,
		"connectionAppliedWithoutDaemonStop": true,
		"runCompletedThroughProxy":           true,
		"helpJourneysRendered":               true,
		"tuiSnapshotRendered":                true,
		"tuiPTYExitedCleanly":                true,
		"webUIAuthenticated":                 true,
		"environmentCleaned":                 true,
		"sameCandidateUpdate":                true,
		"uninstallDryRun":                    true,
		"uninstallPreservedStore":            true,
		"packageFilesAbsentAfterUninstall":   true,
		"finalReinstallExact":                true,
		"finalDaemonStopped":                 true,
		"finalEnvironmentAbsent":             true,
		"noSecretValueInEvidence":            true,
	}
	local := map[string]any{
		"schema":      "hideout.local-install-candidate/v1",
		"generatedAt": "2026-07-31T00:00:00Z",
		"result":      "passed",
		"sourceCandidate": map[string]any{
			"commit": commit,
			"tree":   tree,
			"dirty":  false,
		},
		"candidateAcceptance": true,
		"candidate": map[string]any{
			"version":                "0.1.0-alpha.4",
			"archive":                "run-fixture/hideout-v0.1.0-alpha.4-darwin-arm64.tar.gz",
			"archiveSHA256":          digest,
			"packageManifestSHA256":  digest,
			"installedBinarySHA256":  digest,
			"consumedWithoutRebuild": true,
		},
		"installation": map[string]any{
			"hostOS":                "Darwin",
			"hostArch":              "arm64",
			"prefix":                "/opt/homebrew",
			"store":                 "/Users/fixture/.hideout",
			"legacyDataPolicy":      "explicitly-discarded",
			"priorInstallation":     "homebrew-v0.1.0-alpha.3",
			"finalInstallation":     "exact-standalone-candidate",
			"finalDaemonState":      "stopped",
			"finalEnvironmentCount": 0,
		},
		"checks": localChecks,
		"artifacts": []any{
			artifact, artifact, artifact, artifact,
			artifact, artifact, artifact, artifact,
		},
		"limitations": []any{"local-only candidate"},
	}
	localSchema := compileFeatureSchema(t, "local-install-candidate.schema.json")
	if err := localSchema.Validate(local); err != nil {
		t.Fatalf("valid local-install receipt: %v", err)
	}
	localChecks["connectionAppliedWithoutDaemonStop"] = false
	if err := localSchema.Validate(local); err == nil {
		t.Fatal("local-install schema accepted a false required check")
	}
	localChecks["connectionAppliedWithoutDaemonStop"] = true

	publication := map[string]any{
		"schema":      "hideout.publication-absence/v1",
		"generatedAt": "2026-07-31T00:00:00Z",
		"result":      "passed",
		"sourceCandidate": map[string]any{
			"commit": commit,
			"tree":   tree,
			"dirty":  false,
		},
		"candidate": map[string]any{
			"version":       "0.1.0-alpha.4",
			"tag":           "v0.1.0-alpha.4",
			"archiveSHA256": digest,
		},
		"candidateArchiveSHA256": digest,
		"observations": map[string]any{
			"remoteTagCreated":     false,
			"githubReleaseCreated": false,
			"homebrewChanged":      false,
			"packagePublished":     false,
		},
		"remote": map[string]any{
			"sourceRepository":   "vibe-agi/hideout",
			"tagQuery":           "refs/tags/v0.1.0-alpha.4",
			"releaseHTTPStatus":  404,
			"homebrewRepository": "vibe-agi/homebrew-tap",
			"formulaPath":        "Formula/hideout.rb",
			"formulaSHA256":      digest,
		},
		"localTap": map[string]any{
			"path":                "/opt/homebrew/Library/Taps/vibe-agi/homebrew-tap",
			"headBefore":          commit,
			"headAfter":           commit,
			"treeBefore":          tree,
			"treeAfter":           tree,
			"formulaSHA256Before": digest,
			"formulaSHA256After":  digest,
			"cleanBefore":         true,
			"cleanAfter":          true,
		},
		"checks": map[string]any{
			"sourceClean":              true,
			"candidateDigestVerified":  true,
			"remoteTagAbsent":          true,
			"githubReleaseAbsent":      true,
			"homebrewFormulaUnchanged": true,
			"homebrewCandidateAbsent":  true,
			"localTapUnchanged":        true,
			"noPublicationMutation":    true,
			"observationsExactlyFalse": true,
		},
		"artifacts":   []any{artifact, artifact, artifact},
		"limitations": []any{"read-only point-in-time observation"},
	}
	publicationSchema := compileFeatureSchema(t, "publication-absence.schema.json")
	if err := publicationSchema.Validate(publication); err != nil {
		t.Fatalf("valid publication-absence receipt: %v", err)
	}
	publication["observations"].(map[string]any)["githubReleaseCreated"] = true
	if err := publicationSchema.Validate(publication); err == nil {
		t.Fatal("publication-absence schema accepted an observed release")
	}
}

func compileFeatureSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	added := map[string]bool{}
	for _, dependency := range []string{name, "activity-owner.schema.json"} {
		if added[dependency] {
			continue
		}
		added[dependency] = true
		data, err := os.ReadFile(dependency)
		if err != nil {
			t.Fatalf("read %s: %v", dependency, err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("parse %s: %v", dependency, err)
		}
		if err := compiler.AddResource(schemaBaseURL+dependency, document); err != nil {
			t.Fatalf("add %s: %v", dependency, err)
		}
	}
	schema, err := compiler.Compile(schemaBaseURL + name)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return schema
}

func reusableOwnerFixture() map[string]any {
	return map[string]any{
		"kind":                 "reusable-environment",
		"environmentId":        "env_fixture",
		"backend":              "lima",
		"backendIncarnationId": "machine-fixture-1",
	}
}
