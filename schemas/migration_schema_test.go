package schemas_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationAutomationSchemaAcceptsSharedGoldenExportPlan(t *testing.T) {
	encoded, err := os.ReadFile("../internal/migration/testdata/export-plan-surface-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if err := compileFeatureSchema(t, "migration-plan.schema.json").Validate(document); err != nil {
		t.Fatalf("shared automation export plan: %v", err)
	}
}

func TestMigrationSchemasAcceptRepresentativeDocuments(t *testing.T) {
	manifestSchema := compileFeatureSchema(t, "migration-manifest.schema.json")
	if err := manifestSchema.Validate(migrationManifestFixture()); err != nil {
		t.Fatalf("valid migration manifest: %v", err)
	}
	attachedManifest := migrationManifestFixture()
	attachedManifest["diskEdges"] = append(
		attachedManifest["diskEdges"].([]any),
		migrationSchemaDocument(t, migration.DiskEdge{
			EnvironmentRef: "envref_dev1234", DiskID: "disk_attached1234",
			Attachment: migration.DiskRoleAttached, GuestPath: "/mnt/lima-source-data",
			FSType: "ext4", ReadOnly: false,
		}),
	)
	if err := manifestSchema.Validate(attachedManifest); err != nil {
		t.Fatalf("valid attached-disk manifest edge: %v", err)
	}

	planSchema := compileFeatureSchema(t, "migration-plan.schema.json")
	for name, fixture := range map[string]any{
		"export request": migrationExportRequestFixture(),
		"export plan":    migrationExportPlanFixture(),
		"import draft":   migrationImportDraftFixture(),
		"import plan":    migrationImportPlanFixture(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := planSchema.Validate(fixture); err != nil {
				t.Fatalf("valid %s: %v", name, err)
			}
		})
	}
	receiptSchema := compileFeatureSchema(t, "migration-receipt.schema.json")
	for name, fixture := range map[string]any{
		"safe clone request": migrationAdoptionRequestFixture(),
		"completed receipt":  migrationAdoptionReceiptFixture(),
		"mounted request":    migrationMountedAdoptionRequestFixture(),
		"mounted receipt":    migrationMountedAdoptionReceiptFixture(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := receiptSchema.Validate(fixture); err != nil {
				t.Fatalf("valid %s: %v", name, err)
			}
		})
	}
	for name, document := range migrationProductionAdoptionDocuments(t) {
		t.Run("production "+name, func(t *testing.T) {
			if err := receiptSchema.Validate(document); err != nil {
				t.Fatalf("production %s drifted from its JSON schema: %v", name, err)
			}
		})
	}

	projectionSchema := compileFeatureSchema(
		t, "migration-operation-projection.schema.json",
	)
	if err := projectionSchema.Validate(migrationOperationProjectionFixture()); err != nil {
		t.Fatalf("valid migration operation projection: %v", err)
	}
}

func TestMigrationSchemasRejectUnknownFieldsSecretsAndBounds(t *testing.T) {
	manifestSchema := compileFeatureSchema(t, "migration-manifest.schema.json")
	manifest := migrationManifestFixture()
	manifest["passphrase"] = "must-not-be-serializable"
	if err := manifestSchema.Validate(manifest); err == nil {
		t.Fatal("manifest accepted a passphrase field")
	}

	manifest = migrationManifestFixture()
	secret := manifest["secretEntries"].([]any)[0].(map[string]any)
	secret["value"] = "must-not-be-plaintext"
	if err := manifestSchema.Validate(manifest); err == nil {
		t.Fatal("manifest accepted an inline secret value")
	}

	manifest = migrationManifestFixture()
	environment := manifest["environments"].([]any)[0]
	environments := make([]any, 33)
	for index := range environments {
		environments[index] = environment
	}
	manifest["environments"] = environments
	if err := manifestSchema.Validate(manifest); err == nil {
		t.Fatal("manifest accepted more than 32 environments")
	}

	manifest = migrationManifestFixture()
	delete(
		manifest["environments"].([]any)[0].(map[string]any),
		"profileStateComponentId",
	)
	if err := manifestSchema.Validate(manifest); err == nil {
		t.Fatal("full manifest accepted a missing profile application state")
	}

	manifest = migrationManifestFixture()
	manifest["environments"].([]any)[0].(map[string]any)["mode"] = "config"
	if err := manifestSchema.Validate(manifest); err == nil {
		t.Fatal("config manifest accepted a profile application state component")
	}

	manifest = migrationManifestFixture()
	manifest["diskEdges"] = []any{map[string]any{
		"environmentRef": "envref_dev1234", "diskId": "disk_attached1234",
		"attachment": "attached", "guestPath": "/mnt/lima-source-data",
		"readOnly": false,
	}}
	if err := manifestSchema.Validate(manifest); err == nil {
		t.Fatal("attached-disk manifest edge omitted its filesystem type")
	}

	planSchema := compileFeatureSchema(t, "migration-plan.schema.json")
	exportPlan := migrationExportPlanFixture()
	delete(
		exportPlan["environmentEstimates"].([]any)[0].(map[string]any),
		"profileStateDigest",
	)
	if err := planSchema.Validate(exportPlan); err == nil {
		t.Fatal("full export plan accepted incomplete profile application state evidence")
	}

	exportPlan = migrationExportPlanFixture()
	exportPlan["mode"] = "config"
	if err := planSchema.Validate(exportPlan); err == nil {
		t.Fatal("config export plan accepted profile application state evidence")
	}

	plan := migrationImportPlanFixture()
	plan["secretInputHandle"] = "one-shot-handle-must-not-be-durable"
	if err := planSchema.Validate(plan); err == nil {
		t.Fatal("durable import plan accepted a secret-input handle")
	}

	plan = migrationImportPlanFixture()
	delete(
		plan["environmentActions"].([]any)[0].(map[string]any),
		"profileStateContentDigest",
	)
	if err := planSchema.Validate(plan); err == nil {
		t.Fatal("import plan accepted a partially bound profile application state")
	}

	draft := migrationImportDraftFixture()
	draft["selectedEnvironmentRefs"] = make([]any, 33)
	for index := range draft["selectedEnvironmentRefs"].([]any) {
		draft["selectedEnvironmentRefs"].([]any)[index] = "env_too_many"
	}
	if err := planSchema.Validate(draft); err == nil {
		t.Fatal("import draft accepted more than 32 environments")
	}

	receiptSchema := compileFeatureSchema(t, "migration-receipt.schema.json")
	request := migrationAdoptionRequestFixture()
	request["command"] = "rm -rf /"
	if err := receiptSchema.Validate(request); err == nil {
		t.Fatal("adoption request accepted an executable command")
	}

	request = migrationAdoptionRequestFixture()
	request["permittedActions"] = []any{"preserve-guest-identity"}
	if err := receiptSchema.Validate(request); err == nil {
		t.Fatal("Safe Clone request accepted Exact Restore actions")
	}

	request = migrationMountedAdoptionRequestFixture()
	request["mountBindings"].([]any)[0].(map[string]any)["fsType"] = "swap"
	if err := receiptSchema.Validate(request); err == nil {
		t.Fatal("adoption request accepted a swap mount binding")
	}

	receipt := migrationMountedAdoptionReceiptFixture()
	receipt["actionResults"] = receipt["actionResults"].([]any)[:3]
	if err := receiptSchema.Validate(receipt); err == nil {
		t.Fatal("completed mounted receipt omitted destination key installation")
	}
	receipt = migrationMountedAdoptionReceiptFixture()
	receipt["actionResults"].([]any)[2].(map[string]any)["action"] =
		"preserve-guest-identity"
	if err := receiptSchema.Validate(receipt); err == nil {
		t.Fatal("completed mounted receipt omitted mount rebinding")
	}

	projectionSchema := compileFeatureSchema(
		t, "migration-operation-projection.schema.json",
	)
	projection := migrationOperationProjectionFixture()
	projection["providerHandle"] = "/Users/example/private/provider-state"
	if err := projectionSchema.Validate(projection); err == nil {
		t.Fatal("operator projection accepted a provider path/handle")
	}

	projection = migrationOperationProjectionFixture()
	projection["progress"].(map[string]any)["completedLogicalBytes"] =
		uint64(4398046511105)
	if err := projectionSchema.Validate(projection); err == nil {
		t.Fatal("operator projection accepted progress above the hard bound")
	}

	projection = migrationOperationProjectionFixture()
	projection["recovery"].(map[string]any)["required"] = true
	if err := projectionSchema.Validate(projection); err == nil {
		t.Fatal("operator projection accepted recovery without one exact action")
	}
}

func migrationOperationProjectionFixture() map[string]any {
	return map[string]any{
		"schema":      "hideout.migration-operation-projection/v1",
		"operationId": "op_projection1234",
		"revision":    3,
		"bundleId":    "migb_projection1234",
		"kind":        "import",
		"state":       "materializing",
		"phaseLabel":  "Copying persistent data",
		"progress": map[string]any{
			"logicalTotalKnown": true, "completedLogicalBytes": 1024,
			"totalLogicalBytes": 4096, "encodedTotalKnown": false,
			"completedEncodedBytes": 512, "componentsComplete": 1,
			"componentsTotal": 2, "currentItem": "environment 1 of 2",
			"phaseStartedAt": "2026-08-03T00:00:00Z",
			"checkpointAt":   "2026-08-03T00:00:01Z",
			"elapsedSeconds": 1, "remainingKnown": true,
			"remainingSeconds": 3, "cancelPending": false,
		},
		"recovery": map[string]any{
			"required": false, "code": "migration.operation.none",
			"allowedActions": []any{},
		},
		"warnings": []any{},
		"effects": []any{
			map[string]any{"kind": "stage", "status": "running"},
		},
		"identityPolicies": map[string]any{
			"safeClone": 1, "exactGuestRestore": 0,
			"freshControl": 1, "freshBackend": 1,
		},
	}
}

func TestMigrationIdentityPolicyIsChosenPerImportNotExport(t *testing.T) {
	schema := compileFeatureSchema(t, "migration-plan.schema.json")
	exportRequest := migrationExportRequestFixture()
	exportRequest["identityPolicies"] = []any{map[string]any{
		"sourceRef": "envref_dev1234", "policy": "safe-clone",
	}}
	if err := schema.Validate(exportRequest); err == nil {
		t.Fatal("export request accepted a destination guest identity policy")
	}

	safeClone := migrationImportDraftFixture()
	exactRestore := migrationImportDraftFixture()
	exactRestore["identityPolicies"] = []any{map[string]any{
		"sourceRef": "envref_dev1234", "policy": "exact-guest-restore",
	}}
	if !reflect.DeepEqual(safeClone["bundleBinding"], exactRestore["bundleBinding"]) {
		t.Fatal("fixture did not reuse the exact same sealed bundle binding")
	}
	for name, draft := range map[string]map[string]any{
		"safe clone":          safeClone,
		"exact guest restore": exactRestore,
	} {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(draft); err != nil {
				t.Fatalf("same bundle could not use %s at import: %v", name, err)
			}
		})
	}
}

func TestMigrationWorkspaceAuthorityIsExplicitAndIdentityBound(t *testing.T) {
	schema := compileFeatureSchema(t, "migration-plan.schema.json")
	mapped := migrationImportPlanFixture()
	mapped["workspaceActions"] = []any{map[string]any{
		"proposalId": "proposal_workspace1234", "environmentRef": "envref_dev1234",
		"guestPath": "/workspace", "decision": "mapped",
		"destinationPath": "/Users/example/project", "rootDevice": 42, "rootInode": 84,
	}}
	if err := schema.Validate(mapped); err != nil {
		t.Fatalf("identity-bound workspace action was rejected: %v", err)
	}

	disabledWithPath := migrationImportPlanFixture()
	disabledWithPath["workspaceActions"] = []any{map[string]any{
		"proposalId": "proposal_workspace1234", "environmentRef": "envref_dev1234",
		"guestPath": "/workspace", "decision": "disabled",
		"destinationPath": "/Users/example/project",
	}}
	if err := schema.Validate(disabledWithPath); err == nil {
		t.Fatal("disabled workspace action retained destination path authority")
	}

	unproved := migrationImportPlanFixture()
	unproved["workspaceActions"] = []any{map[string]any{
		"proposalId": "proposal_workspace1234", "environmentRef": "envref_dev1234",
		"guestPath": "/workspace", "decision": "mapped",
		"destinationPath": "/Users/example/project",
	}}
	if err := schema.Validate(unproved); err == nil {
		t.Fatal("mapped workspace action omitted real directory identity")
	}
}

func migrationManifestFixture() map[string]any {
	return map[string]any{
		"schema":        "hideout.migration-manifest/v1",
		"bundleId":      "migb_fixture1234",
		"formatVersion": 1,
		"sourceProduct": map[string]any{
			"version": "0.1.0-alpha.4",
			"hostOS":  "darwin", "hostArch": "arm64",
			"backend": "lima", "backendVersion": "2.2.0",
			"guestArch": "aarch64",
		},
		"environments": []any{map[string]any{
			"sourceEnvironmentRef":    "envref_dev1234",
			"displayNameHint":         "dev",
			"runtime":                 "linux",
			"backend":                 "lima",
			"mode":                    "full",
			"profileComponentId":      "component_profile1234",
			"profileStateComponentId": "component_state12345",
			"workspaceProposals": []any{map[string]any{
				"proposalId":   "proposal_workspace1234",
				"guestPath":    "/workspace",
				"hostPathHint": "/Users/example/project",
				"state":        "disabled",
			}},
			"authorityProposalRefs": []any{"proposal_network1234"},
			"guestIdentityEvidence": map[string]any{
				"machineIdDigest":   migrationDigestFixture("1"),
				"sshHostKeyDigests": []any{migrationDigestFixture("2")},
			},
			"diskRefs": []any{"disk_root1234"},
		}},
		"diskObjects": []any{map[string]any{
			"diskId": "disk_root1234", "role": "root", "format": "qcow2",
			"logicalBytes": 1073741824, "allocatedBytesHint": 536870912,
			"contentDigest": migrationDigestFixture("3"),
			"provider": map[string]any{
				"name": "lima", "kind": "instance-root",
				"features": []any{"sparse", "copy-on-write"},
			},
		}},
		"diskEdges": []any{map[string]any{
			"environmentRef": "envref_dev1234", "diskId": "disk_root1234",
			"attachment": "root", "guestPath": "/", "readOnly": false,
		}},
		"secretEntries": []any{map[string]any{
			"secretRef": "secretref_proxy1234", "displayName": "local-proxy",
			"provider": "keychain", "transfer": "reference-only",
		}},
		"authorityProposals": []any{map[string]any{
			"proposalId": "proposal_network1234", "class": "network",
			"sourceSummary": "proxy configuration", "state": "disabled",
		}},
		"componentIndex": []any{
			map[string]any{
				"componentId": "component_profile1234", "kind": "profile",
				"logicalBytes": 1024, "firstRecord": 0, "lastRecord": 0,
				"recordCount": 1, "contentDigest": migrationDigestFixture("4"),
			},
			map[string]any{
				"componentId": "component_state12345", "kind": "profile-state",
				"logicalBytes": 512, "firstRecord": 1, "lastRecord": 1,
				"recordCount": 1, "contentDigest": migrationDigestFixture("9"),
			},
			map[string]any{
				"componentId": "component_disk1234", "kind": "disk",
				"diskId": "disk_root1234", "logicalBytes": 1073741824,
				"firstRecord": 2, "lastRecord": 257, "recordCount": 256,
				"contentDigest": migrationDigestFixture("3"),
			},
		},
		"excludedClasses": []any{
			"host-workspace-content", "activity-history", "runtime-state",
		},
		"requiredCapabilities": []any{map[string]any{
			"id": "persistent-disk-import", "provider": "lima",
			"minimumVersion": "2.1.0",
		}},
	}
}

func migrationExportRequestFixture() map[string]any {
	return map[string]any{
		"schema": "hideout.migration-export-request/v1",
		"mode":   "full", "environmentNames": []any{"dev"},
		"includeSecretRefs": []any{},
		"outputPath":        "/tmp/dev.hideout-migration",
		"riskAcknowledgements": []any{
			"migration.content.opaque_guest_disk_sensitive",
		},
	}
}

func migrationExportPlanFixture() map[string]any {
	return map[string]any{
		"schema": "hideout.migration-export-plan/v1",
		"planId": "migplan_export1234", "planDigest": migrationDigestFixture("5"),
		"baseRevisions": []any{map[string]any{
			"resource": "environment:dev", "revision": 7,
			"digest": migrationDigestFixture("6"),
		}},
		"mode": "full", "environmentRefs": []any{"envref_dev1234"},
		"diskRefs": []any{"disk_root1234"}, "selectedSecretRefs": []any{},
		"includedClasses": []any{
			"environment-declarations", "persistent-disks", "portable-profiles",
			"profile-application-state",
		},
		"excludedClasses": []any{"host-workspace-content"},
		"environmentEstimates": []any{map[string]any{
			"environmentRef": "envref_dev1234", "displayName": "dev",
			"portableConfigLogicalBytes": 1024,
			"portableConfigDigest":       migrationDigestFixture("4"),
			"profileStateLogicalBytes":   512,
			"profileStateDigest":         migrationDigestFixture("9"),
			"diskRefs":                   []any{"disk_root1234"},
			"referencedDiskLogicalBytes": 1073741824,
			"estimatedLogicalBytes":      1073743360,
		}},
		"diskEstimates": []any{map[string]any{
			"diskRef": "disk_root1234", "role": "root",
			"logicalBytes": 1073741824, "allocatedBytesHint": 536870912,
			"consumers": []any{"envref_dev1234"},
		}},
		"estimatedPayloadLogicalBytes": 1073743360,
		"estimatedPayloadComplete":     true,
		"outputPath":                   "/tmp/dev.hideout-migration",
		"providerCapabilityRevision":   migrationDigestFixture("7"),
		"sourceInventoryDigest":        migrationDigestFixture("8"),
		"warnings":                     []any{},
		"effects": []any{map[string]any{
			"id": "effect_snapshot1234", "kind": "snapshot-source",
			"provider": "lima", "compensation": "release-snapshot",
		}},
		"confirmationText": "Export the reviewed stopped environment.",
	}
}

func migrationImportDraftFixture() map[string]any {
	return map[string]any{
		"schema":                  "hideout.migration-import-draft/v1",
		"bundlePath":              "/tmp/dev.hideout-migration",
		"bundleBinding":           migrationBundleBindingFixture(),
		"selectedEnvironmentRefs": []any{"envref_dev1234"},
		"nameMappings": []any{map[string]any{
			"sourceRef": "envref_dev1234", "destinationName": "dev-clone",
		}},
		"conflictDecisions": []any{},
		"workspaceMappings": []any{}, "secretMappings": []any{},
		"identityPolicies": []any{map[string]any{
			"sourceRef": "envref_dev1234", "policy": "safe-clone",
		}},
		"authorityDecisions": []any{},
	}
}

func migrationImportPlanFixture() map[string]any {
	return map[string]any{
		"schema": "hideout.migration-import-plan/v1",
		"planId": "migplan_import1234", "planDigest": migrationDigestFixture("8"),
		"bundlePath":    "/tmp/dev.hideout-migration",
		"bundleBinding": migrationBundleBindingFixture(),
		"baseRevisions": []any{map[string]any{
			"resource": "environment-names", "revision": 9,
			"digest": migrationDigestFixture("9"),
		}},
		"compatibility": map[string]any{
			"backend": "lima", "available": true,
			"capabilityRevision": migrationDigestFixture("a"),
			"requiredBytes":      1073741824, "availableBytes": 2147483648,
		},
		"objects": []any{map[string]any{
			"sourceRef": "envref_dev1234", "destinationName": "dev-clone",
			"mode": "full", "diskRefs": []any{"disk_root1234"},
		}},
		"conflictActions": []any{},
		"environmentActions": []any{map[string]any{
			"sourceRef": "envref_dev1234", "destinationProfileName": "dev-clone",
			"runtime":   "linux",
			"guestUser": "developer", "backend": "lima",
			"profileComponentId":        "component_profile1234",
			"profileContentDigest":      migrationDigestFixture("4"),
			"profileLogicalBytes":       1024,
			"profileStateComponentId":   "component_state12345",
			"profileStateContentDigest": migrationDigestFixture("9"),
			"profileStateLogicalBytes":  512,
		}},
		"identityActions": []any{map[string]any{
			"sourceRef": "envref_dev1234", "guestPolicy": "safe-clone",
			"freshControlIdentity": true, "freshBackendIdentity": true,
		}},
		"workspaceActions":     []any{},
		"authorityActions":     []any{},
		"disabledProposals":    []any{"proposal_network1234"},
		"riskAcknowledgements": []any{},
		"effects": []any{map[string]any{
			"id": "effect_stage1234", "kind": "stage-destination",
			"provider": "lima", "compensation": "rollback-stage",
		}},
		"blockers": []any{},
	}
}

func migrationAdoptionRequestFixture() map[string]any {
	return map[string]any{
		"schema":      "hideout.migration-adoption-request/v1",
		"operationId": "op_import1234", "environmentRef": "envref_dev1234",
		"requestNonce": "nonce_request1234", "receiptNonce": "nonce_receipt1234",
		"policy": "safe-clone",
		"sourceIdentity": map[string]any{
			"machineIdDigest":   migrationDigestFixture("1"),
			"sshHostKeyDigests": []any{migrationDigestFixture("2")},
		},
		"destinationSSHUser": "hideout",
		"destinationSSHKeys": []any{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture"},
		"permittedActions": []any{
			"reset-machine-id", "reset-ssh-host-keys",
			"install-destination-ssh-keys",
		},
		"helper": migrationHelperBindingFixture(),
	}
}

func migrationMountedAdoptionRequestFixture() map[string]any {
	request := migrationAdoptionRequestFixture()
	request["mountBindings"] = migrationMountBindingsFixture()
	request["permittedActions"] = []any{
		"reset-machine-id", "reset-ssh-host-keys",
		"rebind-attached-disk-mounts", "install-destination-ssh-keys",
	}
	return request
}

func migrationAdoptionReceiptFixture() map[string]any {
	return map[string]any{
		"schema":      "hideout.migration-adoption-receipt/v1",
		"operationId": "op_import1234", "environmentRef": "envref_dev1234",
		"requestNonce": "nonce_request1234", "receiptNonce": "nonce_receipt1234",
		"policy": "safe-clone", "helper": migrationHelperBindingFixture(),
		"actionResults": []any{
			map[string]any{"action": "reset-machine-id", "status": "completed"},
			map[string]any{"action": "reset-ssh-host-keys", "status": "completed"},
			map[string]any{"action": "install-destination-ssh-keys", "status": "completed"},
		},
		"postIdentity": map[string]any{
			"machineIdDigest":   migrationDigestFixture("b"),
			"sshHostKeyDigests": []any{migrationDigestFixture("c")},
		},
		"status": "completed", "completionMarker": true,
	}
}

func migrationMountedAdoptionReceiptFixture() map[string]any {
	receipt := migrationAdoptionReceiptFixture()
	receipt["mountBindings"] = migrationMountBindingsFixture()
	receipt["actionResults"] = []any{
		map[string]any{"action": "reset-machine-id", "status": "completed"},
		map[string]any{"action": "reset-ssh-host-keys", "status": "completed"},
		map[string]any{"action": "rebind-attached-disk-mounts", "status": "completed"},
		map[string]any{"action": "install-destination-ssh-keys", "status": "completed"},
	}
	return receipt
}

func migrationMountBindingsFixture() []any {
	return []any{map[string]any{
		"diskId":               "disk_attached1234",
		"sourceGuestPath":      "/mnt/lima-source-data",
		"destinationGuestPath": "/mnt/lima-disk_destination1234",
		"fsType":               "ext4",
	}}
}

func migrationProductionAdoptionDocuments(t *testing.T) map[string]any {
	t.Helper()
	source := migration.GuestIdentityEvidence{
		MachineIDDigest: migration.Digest(migrationDigestFixture("1")),
		SSHHostKeyDigests: []migration.Digest{
			migration.Digest(migrationDigestFixture("2")),
		},
	}
	post := migration.GuestIdentityEvidence{
		MachineIDDigest: migration.Digest(migrationDigestFixture("b")),
		SSHHostKeyDigests: []migration.Digest{
			migration.Digest(migrationDigestFixture("c")),
		},
	}
	helper := migration.HelperBinding{
		PackageID: migration.AdoptionHelperPackage, Version: "0.1.0-alpha.4",
		SHA256: migration.Digest(migrationDigestFixture("a")),
	}
	bindings := []migration.DiskMountBinding{{
		DiskID: "disk_attached1234", SourceGuestPath: "/mnt/lima-source-data",
		DestinationGuestPath: "/mnt/lima-disk_destination1234", FSType: "ext4",
	}}
	request := migration.AdoptionRequest{
		Schema: migration.AdoptionRequestSchema, OperationID: "op_import1234",
		EnvironmentRef: "envref_dev1234", RequestNonce: "nonce_request1234",
		ReceiptNonce: "nonce_receipt1234", Policy: migration.GuestIdentitySafeClone,
		SourceIdentity: source, DestinationSSHUser: "hideout",
		DestinationSSHKeys: []string{
			"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture",
		},
		MountBindings: bindings,
		PermittedActions: []string{
			migration.AdoptionActionResetMachineID,
			migration.AdoptionActionResetSSHHostKeys,
			migration.AdoptionActionRebindDiskMounts,
			migration.AdoptionActionInstallSSHKeys,
		},
		Helper: helper,
	}
	receipt := migration.AdoptionReceipt{
		Schema: migration.AdoptionReceiptSchema, OperationID: request.OperationID,
		EnvironmentRef: request.EnvironmentRef, RequestNonce: request.RequestNonce,
		ReceiptNonce: request.ReceiptNonce, Policy: request.Policy, Helper: helper,
		MountBindings: bindings,
		ActionResults: []migration.AdoptionActionResult{
			{Action: migration.AdoptionActionResetMachineID, Status: migration.AdoptionActionStatusCompleted},
			{Action: migration.AdoptionActionResetSSHHostKeys, Status: migration.AdoptionActionStatusCompleted},
			{Action: migration.AdoptionActionRebindDiskMounts, Status: migration.AdoptionActionStatusCompleted},
			{Action: migration.AdoptionActionInstallSSHKeys, Status: migration.AdoptionActionStatusCompleted},
		},
		PostIdentity: &post, Status: migration.AdoptionReceiptStatusCompleted,
		CompletionMarker: true,
	}
	failedReceipt := migration.AdoptionReceipt{
		Schema: migration.AdoptionReceiptSchema, OperationID: request.OperationID,
		EnvironmentRef: request.EnvironmentRef, RequestNonce: request.RequestNonce,
		ReceiptNonce: request.ReceiptNonce, Policy: request.Policy, Helper: helper,
		MountBindings: bindings,
		ActionResults: []migration.AdoptionActionResult{{
			Action: migration.AdoptionActionResetMachineID,
			Status: migration.AdoptionActionStatusFailed,
			Code:   "migration.adoption.machine_id_reset_failed",
		}},
		Status:      migration.AdoptionReceiptStatusFailed,
		FailureCode: "migration.adoption.machine_id_reset_failed",
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("production adoption request fixture: %v", err)
	}
	if err := receipt.MatchesRequest(request); err != nil {
		t.Fatalf("production adoption receipt fixture: %v", err)
	}
	if err := failedReceipt.MatchesRequest(request); err != nil {
		t.Fatalf("production failed adoption receipt fixture: %v", err)
	}
	return map[string]any{
		"request":        migrationSchemaDocument(t, request),
		"receipt":        migrationSchemaDocument(t, receipt),
		"failed receipt": migrationSchemaDocument(t, failedReceipt),
	}
}

func migrationSchemaDocument(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func migrationBundleBindingFixture() map[string]any {
	return map[string]any{
		"bundleId": "migb_fixture1234", "formatVersion": 1,
		"fileDigest":       migrationDigestFixture("d"),
		"manifestDigest":   migrationDigestFixture("e"),
		"completionDigest": migrationDigestFixture("f"),
	}
}

func migrationHelperBindingFixture() map[string]any {
	return map[string]any{
		"packageId": "hideout-migration-adopt", "version": "0.1.0-alpha.4",
		"sha256": migrationDigestFixture("a"),
	}
}

func migrationDigestFixture(character string) string {
	value := ""
	for len(value) < 64 {
		value += character
	}
	return "sha256:" + value[:64]
}
