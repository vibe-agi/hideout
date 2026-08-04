package uiweb_assets

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestBrowserMigrationConsumesSharedGoldenExportPlan(t *testing.T) {
	encoded, err := os.ReadFile("../../migration/testdata/export-plan-surface-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole:{}};
` + mustAsset("migration.js") + `
const plan = JSON.parse(` + strconv.Quote(string(encoded)) + `);
const view = window.HideoutConsole.Migration.exportPlanView(plan);
JSON.stringify(view);
`)
	if err != nil {
		t.Fatalf("render shared browser export plan: %v", err)
	}
	var view struct {
		Included             []string `json:"included"`
		PayloadEstimate      string   `json:"payloadEstimate"`
		EnvironmentEstimates []struct {
			EnvironmentRef string `json:"environmentRef"`
			DisplayName    string `json:"displayName"`
		} `json:"environmentEstimates"`
		DiskEstimates []struct {
			DiskRef   string   `json:"diskRef"`
			Consumers []string `json:"consumers"`
		} `json:"diskEstimates"`
	}
	if err := json.Unmarshal([]byte(value.String()), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Included) != 3 || view.PayloadEstimate != "9.0 KiB (complete logical payload)" ||
		len(view.EnvironmentEstimates) != 1 ||
		view.EnvironmentEstimates[0].EnvironmentRef != "environment_source1" ||
		view.EnvironmentEstimates[0].DisplayName != "dev" ||
		len(view.DiskEstimates) != 1 || view.DiskEstimates[0].DiskRef != "disk_root0000001" ||
		len(view.DiskEstimates[0].Consumers) != 1 ||
		view.DiskEstimates[0].Consumers[0] != "environment_source1" {
		t.Fatalf("browser shared golden plan drifted: %+v", view)
	}
}

func TestBrowserMigrationRendersAuthenticatedConcreteExportInventory(t *testing.T) {
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole:{}};
` + mustAsset("migration.js") + `
const migration = window.HideoutConsole.Migration;
const plan = {
  schema:migration.EXPORT_PLAN_SCHEMA,
  planId:"migplan_browserreview1",planDigest:"sha256:"+"a".repeat(64),
  mode:"full",outputPath:"/tmp/dev.hideout-migration",
  environmentRefs:["environment_source1"],diskRefs:["disk_root0000001"],
  selectedSecretRefs:["local-proxy"],
  includedClasses:[
    "environment-declarations","persistent-disks","portable-profiles",
    "selected-secret-values"
  ],
  excludedClasses:["host-workspace-content"],
  environmentEstimates:[{
    environmentRef:"environment_source1",displayName:"dev",
    portableConfigLogicalBytes:1024,portableConfigDigest:"sha256:"+"b".repeat(64),
    diskRefs:["disk_root0000001"],referencedDiskLogicalBytes:8192,
    estimatedLogicalBytes:9216
  }],
  diskEstimates:[{
    diskRef:"disk_root0000001",role:"root",logicalBytes:8192,
    allocatedBytesHint:4096,consumers:["environment_source1"]
  }],
  estimatedPayloadLogicalBytes:9216,estimatedPayloadComplete:false,
  effects:[],riskAcknowledgements:[],warnings:[],confirmationText:"Review."
};
const view = migration.exportPlanView(plan);
let rejectedMissingInventory = false;
try {
  const invalid = JSON.parse(JSON.stringify(plan));
  delete invalid.environmentEstimates;
  migration.exportPlanView(invalid);
} catch (_) { rejectedMissingInventory = true; }
JSON.stringify({view,rejectedMissingInventory});
`)
	if err != nil {
		t.Fatalf("run browser export inventory: %v", err)
	}
	var proof struct {
		View struct {
			Included             []string `json:"included"`
			PayloadEstimate      string   `json:"payloadEstimate"`
			EnvironmentEstimates []struct {
				DisplayName      string   `json:"displayName"`
				EstimatedLogical uint64   `json:"estimatedLogicalBytes"`
				DiskRefs         []string `json:"diskRefs"`
			} `json:"environmentEstimates"`
			DiskEstimates []struct {
				DiskRef string `json:"diskRef"`
				Role    string `json:"role"`
			} `json:"diskEstimates"`
		} `json:"view"`
		RejectedMissingInventory bool `json:"rejectedMissingInventory"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if !proof.RejectedMissingInventory || len(proof.View.Included) != 4 ||
		!strings.Contains(proof.View.PayloadEstimate, "minimum") ||
		!strings.Contains(proof.View.PayloadEstimate, "secret value sizes hidden") ||
		len(proof.View.EnvironmentEstimates) != 1 ||
		proof.View.EnvironmentEstimates[0].DisplayName != "dev" ||
		proof.View.EnvironmentEstimates[0].EstimatedLogical != 9216 ||
		len(proof.View.EnvironmentEstimates[0].DiskRefs) != 1 ||
		len(proof.View.DiskEstimates) != 1 ||
		proof.View.DiskEstimates[0].Role != "root" {
		t.Fatalf("browser export inventory drifted: %+v", proof)
	}
}

func TestBrowserMigrationProjectsConcreteProgressAndNeverRegressesRevision(
	t *testing.T,
) {
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole:{}};
` + mustAsset("migration.js") + `
const migration = window.HideoutConsole.Migration;
function operation(id,revision,completed) {
  return {
    schema:migration.OPERATION_SCHEMA,
    operationId:id,revision,bundleId:"migb_browserfixture1",
    kind:"export",state:"writing",phaseLabel:"Writing the portable bundle",
    progress:{
      logicalTotalKnown:false,completedLogicalBytes:completed,
      encodedTotalKnown:false,completedEncodedBytes:completed / 2,
      componentsComplete:2,elapsedSeconds:9,
      remainingKnown:false,cancelPending:false,
      checkpointAt:"2026-08-03T10:00:00Z"
    },
    recovery:{required:false,code:"migration.operation.none",allowedActions:[]},
    warnings:[],effects:[],
    identityPolicies:{safeClone:0,exactGuestRestore:0,freshControl:0,freshBackend:0}
  };
}
const current = operation("op_migration_browser001",4,4096);
const stale = operation("op_migration_browser001",3,2048);
const merged = migration.mergeOperations([current],[stale]);
JSON.stringify({
  valid:migration.validOperation(current),
  view:migration.operationView(current),
  merged:merged[0]
});
`)
	if err != nil {
		t.Fatalf("run browser migration projection: %v", err)
	}
	var proof struct {
		Valid bool `json:"valid"`
		View  struct {
			Logical     string `json:"logical"`
			Encoded     string `json:"encoded"`
			Components  string `json:"components"`
			ETA         string `json:"eta"`
			Next        string `json:"next"`
			CurrentItem string `json:"currentItem"`
		} `json:"view"`
		Merged struct {
			Revision int `json:"revision"`
			Progress struct {
				Completed uint64 `json:"completedLogicalBytes"`
			} `json:"progress"`
		} `json:"merged"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if !proof.Valid || proof.View.Logical != "4.0 KiB / total unknown" ||
		proof.View.Encoded != "2.0 KiB / total unknown" ||
		proof.View.Components != "2 / total unknown" ||
		proof.View.ETA != "unknown" || proof.View.CurrentItem == "" ||
		!strings.Contains(proof.View.Next, "encrypted bundle") ||
		proof.Merged.Revision != 4 || proof.Merged.Progress.Completed != 4096 {
		t.Fatalf("browser migration projection drifted: %+v", proof)
	}
}

func TestBrowserMigrationBuildsSafeExplicitExportAndImportDrafts(t *testing.T) {
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole:{}};
` + mustAsset("migration.js") + `
const migration = window.HideoutConsole.Migration;
const configExport = migration.buildExportRequest({
  mode:"config",environmentNames:["dev"],includeSecretRefs:"",
  outputPath:"/tmp/dev.hideout-migration"
});
const fullExport = migration.buildExportRequest({
  mode:"full",environmentNames:["dev"],includeSecretRefs:"api-token",
  outputPath:"/tmp/full.hideout-migration"
});
const inspection = {
  binding:{
    bundleId:"migb_browserfixture1",formatVersion:1,
    fileDigest:"sha256:"+"a".repeat(64),
    manifestDigest:"sha256:"+"b".repeat(64),
    completionDigest:"sha256:"+"c".repeat(64)
  },
  inventory:{
    schema:"hideout.migration-bundle-inspection/v1",
    bundleId:"migb_browserfixture1",formatVersion:1,sealed:true,
    environments:[{
      sourceRef:"environment_source1",displayNameHint:"dev-clone",
      workspaceProposals:[{
        proposalId:"workspace_proposal01",guestPath:"/workspace",
        hostPathHint:"/source/not-authority",state:"disabled"
      }]
    }],
    secrets:[{
      secretRef:"secret_source001",displayName:"API token",
      valueIncluded:true
    }],
    authorityProposals:[{
      proposalId:"authority_network01",class:"network",
      sourceSummary:'{"mode":"direct"}'
    }]
  }
};
const choices = migration.importChoices(inspection);
const defaults = JSON.parse(JSON.stringify(choices));
choices.environments[0].selected = true;
const safeDraft = migration.buildImportDraft(
  inspection,"/tmp/dev.hideout-migration",choices
);
choices.environments[0].policy = "exact-guest-restore";
choices.secrets[0].decision = "import-value";
choices.secrets[0].destinationRef = "api-token";
choices.authorities[0].decision = "approved";
choices.authorities[0].destinationValue = '{"mode":"direct"}';
const elevatedDraft = migration.buildImportDraft(
  inspection,"/tmp/dev.hideout-migration",choices
);
JSON.stringify({configExport,fullExport,defaults,safeDraft,elevatedDraft});
`)
	if err != nil {
		t.Fatalf("run browser migration choices: %v", err)
	}
	var proof struct {
		ConfigExport struct {
			Risks []string `json:"riskAcknowledgements"`
		} `json:"configExport"`
		FullExport struct {
			Risks []string `json:"riskAcknowledgements"`
		} `json:"fullExport"`
		Defaults struct {
			Environments []struct {
				Selected bool   `json:"selected"`
				Policy   string `json:"policy"`
			} `json:"environments"`
			Workspaces []struct {
				Decision string `json:"decision"`
			} `json:"workspaces"`
			Secrets []struct {
				Decision string `json:"decision"`
			} `json:"secrets"`
			Authorities []struct {
				Decision string `json:"decision"`
			} `json:"authorities"`
		} `json:"defaults"`
		SafeDraft struct {
			Risks      []string `json:"riskAcknowledgements"`
			Identities []struct {
				Policy string `json:"policy"`
			} `json:"identityPolicies"`
			Workspaces []struct {
				Decision string `json:"decision"`
			} `json:"workspaceMappings"`
			Secrets []struct {
				Decision string `json:"decision"`
			} `json:"secretMappings"`
			Authority []struct {
				Decision string `json:"decision"`
			} `json:"authorityDecisions"`
		} `json:"safeDraft"`
		ElevatedDraft struct {
			Risks []string `json:"riskAcknowledgements"`
		} `json:"elevatedDraft"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if len(proof.ConfigExport.Risks) != 0 || len(proof.FullExport.Risks) != 2 ||
		len(proof.Defaults.Environments) != 1 || proof.Defaults.Environments[0].Selected ||
		proof.Defaults.Environments[0].Policy != "safe-clone" ||
		proof.Defaults.Workspaces[0].Decision != "disabled" ||
		proof.Defaults.Secrets[0].Decision != "unresolved" ||
		proof.Defaults.Authorities[0].Decision != "disabled" ||
		len(proof.SafeDraft.Risks) != 0 ||
		proof.SafeDraft.Identities[0].Policy != "safe-clone" ||
		proof.SafeDraft.Workspaces[0].Decision != "disabled" ||
		proof.SafeDraft.Secrets[0].Decision != "unresolved" ||
		proof.SafeDraft.Authority[0].Decision != "disabled" ||
		len(proof.ElevatedDraft.Risks) != 2 {
		t.Fatalf("browser migration safe defaults drifted: %+v", proof)
	}
}

func TestBrowserMigrationNeverUsesURLOrBrowserStorageForSecrets(t *testing.T) {
	for _, name := range []string{"migration.js", "client.js", "app.js"} {
		source := mustAsset(name)
		for _, forbidden := range []string{
			"localStorage", "sessionStorage", "indexedDB", "document.cookie",
			"passphrase=", "password=",
		} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains forbidden secret persistence/URL marker %q", name, forbidden)
			}
		}
	}
	app := mustAsset("app.js")
	for _, marker := range []string{
		`password.type = "password"`,
		`password.value = ""`,
		`secretInputHandle`,
		`}, 2000);`,
	} {
		if !strings.Contains(app, marker) {
			t.Fatalf("browser migration safety behavior missing %q", marker)
		}
	}
}
