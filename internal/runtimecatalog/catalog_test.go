package runtimecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseAndResolveCatalog(t *testing.T) {
	contract := testContract(t)
	catalog := testCatalog(t, contract)

	parsed, err := Parse(catalog, contract)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := parsed.Resolve(Selection{Family: "developer-standard", HostOS: "darwin", HostArch: "arm64"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Revision.ID != "2026.07.0" || resolved.Artifact.GuestArch != "aarch64" {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
	wantRef := "https://github.com/vibe-agi/hideout/releases/download/runtime-2026.07.0/developer-standard-2026.07.0-linux-aarch64.qcow2#sha256:" + strings.Repeat("a", 64)
	if resolved.ImageRef != wantRef {
		t.Fatalf("ImageRef=%q want %q", resolved.ImageRef, wantRef)
	}
	if resolved.Provenance.ContractDigest != resolved.Revision.ContractDigest || resolved.Provenance.ArtifactSHA256 != strings.Repeat("a", 64) ||
		resolved.Provenance.PackageInventoryDigest != resolved.Artifact.PackageInventoryDigest ||
		resolved.Provenance.PackageInventoryDigest != "sha256:"+strings.Repeat("e", 64) {
		t.Fatalf("provenance did not bind resolution: %+v", resolved.Provenance)
	}
}

func TestParseCatalogRejectsUnsafeAndAmbiguousInputs(t *testing.T) {
	contract := testContract(t)
	base := testCatalog(t, contract)
	type testCase struct {
		want   string
		mutate func(map[string]any)
	}
	cases := map[string]testCase{
		"duplicate family": {
			want: "duplicate family",
			mutate: func(doc map[string]any) {
				families := doc["families"].([]any)
				doc["families"] = append(families, cloneJSON(t, families[0]))
			},
		},
		"unknown field": {
			want: "unknown field",
			mutate: func(doc map[string]any) {
				doc["families"].([]any)[0].(map[string]any)["surprise"] = true
			},
		},
		"moving URL": {
			want: "moving",
			mutate: func(doc map[string]any) {
				artifactMap(t, doc)["location"] = "https://github.com/vibe-agi/hideout/releases/download/latest/developer-standard.qcow2"
			},
		},
		"userinfo URL": {
			want: "userinfo",
			mutate: func(doc map[string]any) {
				artifactMap(t, doc)["location"] = "https://user@github.com/vibe-agi/hideout/releases/download/runtime-2026.07.0/developer-standard.qcow2"
			},
		},
		"architecture ambiguity": {
			want: "duplicate artifact",
			mutate: func(doc map[string]any) {
				revision := revisionMap(t, doc)
				artifacts := revision["artifacts"].([]any)
				revision["artifacts"] = append(artifacts, cloneJSON(t, artifacts[0]))
			},
		},
		"withdrawn current": {
			want: "withdrawn",
			mutate: func(doc map[string]any) {
				revisionMap(t, doc)["status"] = "withdrawn"
			},
		},
		"contract digest drift": {
			want: "contract digest",
			mutate: func(doc map[string]any) {
				revisionMap(t, doc)["contractDigest"] = "sha256:" + strings.Repeat("0", 64)
			},
		},
		"missing package inventory digest": {
			want: "package inventory digest",
			mutate: func(doc map[string]any) {
				delete(artifactMap(t, doc), "packageInventoryDigest")
			},
		},
		"malformed package inventory digest": {
			want: "package inventory digest",
			mutate: func(doc map[string]any) {
				artifactMap(t, doc)["packageInventoryDigest"] = "sha256:" + strings.Repeat("E", 64)
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(base, &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(doc)
			body, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Parse(body, contract)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}
}

func TestParseContractRejectsExecutableOrUnboundedProbeShapes(t *testing.T) {
	base, err := ParseContract(testContract(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Contract){
		"shell":         func(c *Contract) { c.Observations[1].Command = "sh" },
		"path":          func(c *Contract) { c.Observations[1].Command = "/usr/bin/git" },
		"shell flag":    func(c *Contract) { c.Observations[1].VersionArgs = []string{"-c"} },
		"metacharacter": func(c *Contract) { c.Observations[1].VersionArgs = []string{"--version;id"} },
		"unanchored":    func(c *Contract) { c.Observations[1].OutputPattern = "git version" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Observations = append([]Observation(nil), base.Observations...)
			mutate(&candidate)
			body, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseContract(body); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
	unknown := strings.Replace(string(testContract(t)), `"description": "baseline.git"`, `"description": "baseline.git", "install": "apt"`, 1)
	if _, err := ParseContract([]byte(unknown)); err == nil {
		t.Fatal("unknown field should fail")
	}
}

func TestResolveRejectsUnsupportedAndExplicitAmbiguity(t *testing.T) {
	contract := testContract(t)
	parsed, err := Parse(testCatalog(t, contract), contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []Selection{
		{Family: "unknown", HostOS: "darwin", HostArch: "arm64"},
		{Family: "developer-standard", Revision: "unknown", HostOS: "darwin", HostArch: "arm64"},
		{Family: "developer-standard", HostOS: "darwin", HostArch: "amd64"},
		{Family: "developer-standard", HostOS: "darwin", HostArch: "arm64", ImageRef: "https://example.com/x.qcow2#sha256:" + strings.Repeat("a", 64)},
	} {
		if _, err := parsed.Resolve(selection); err == nil {
			t.Fatalf("selection should fail: %+v", selection)
		}
	}
}

func testContract(t *testing.T) []byte {
	t.Helper()
	contract := Contract{Schema: ContractSchema, ID: "developer-standard/v1"}
	for _, required := range V1RequiredObservations() {
		observation := Observation{ID: required.ID, Class: required.Class, Command: required.Command, Description: required.ID}
		if required.Command == "git" {
			observation.VersionArgs = []string{"--version"}
			observation.OutputPattern = "^git version .+$"
		}
		contract.Observations = append(contract.Observations, observation)
	}
	body, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestV1PromotionRejectsUnsupportedTupleAndIncompleteBaseline(t *testing.T) {
	contract := testContract(t)
	base := testCatalog(t, contract)
	for name, mutate := range map[string]func(map[string]any){
		"linux amd64": func(doc map[string]any) {
			artifact := artifactMap(t, doc)
			artifact["hostOS"] = "linux"
			artifact["hostArch"] = "amd64"
			artifact["guestArch"] = "x86_64"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(base, &doc); err != nil {
				t.Fatal(err)
			}
			mutate(doc)
			body, _ := json.Marshal(doc)
			parsed, err := Parse(body, contract)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parsed.Resolve(Selection{Family: "developer-standard", HostOS: "linux", HostArch: "amd64"}); err == nil {
				t.Fatal("unsupported v1 tuple reached promotion")
			}
		})
	}
	parsed, err := Parse(base, contract)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Contract.Observations = parsed.Contract.Observations[1:]
	if err := parsed.ValidateV1Promotable(); err == nil || !strings.Contains(err.Error(), "boundary.getent") {
		t.Fatalf("incomplete baseline promotion error=%v", err)
	}
}

func TestEmbeddedPromotedCatalogResolvesExactReviewedArtifact(t *testing.T) {
	catalog, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateV1Promotable(); err != nil {
		t.Fatalf("embedded catalog is not promotable: %v", err)
	}
	resolved, err := catalog.Resolve(Selection{Family: "developer-standard", HostOS: "darwin", HostArch: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Revision.ID != "2026.07.0" ||
		resolved.Artifact.Location != "https://github.com/vibe-agi/hideout/releases/download/runtime-developer-standard-2026.07.0/developer-standard-2026.07.0-linux-aarch64.qcow2" ||
		resolved.Artifact.SHA256 != "79e5d25bfd05c27b4ee7f2ad085d45c15a63aadbe2ab8d1b4ba2c426e1586134" ||
		resolved.Artifact.Source.SourceLockSHA256 != "5357ebfb2fe8984a71acbfc558597d4ff721970cdd4ef955d75a3c80b6012420" {
		t.Fatalf("embedded artifact drifted: %+v", resolved.Artifact)
	}
}

func testCatalog(t *testing.T, contract []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(contract)
	digest := hex.EncodeToString(sum[:])
	return []byte(fmt.Sprintf(`{
  "schema": "hideout.runtime-catalog/v1",
  "catalogRelease": "2026.07.0",
  "generatedAt": "2026-07-11T00:00:00Z",
  "families": [
    {
      "id": "developer-standard",
      "displayName": "Developer Standard",
      "maturity": "preview",
      "currentRevision": "2026.07.0",
      "revisions": [
        {
          "id": "2026.07.0",
          "status": "preview",
          "contractId": "developer-standard/v1",
          "contractDigest": "sha256:%s",
          "reviewedAt": "2026-07-11T00:00:00Z",
          "artifacts": [
            {
              "hostOS": "darwin",
              "hostArch": "arm64",
              "guestArch": "aarch64",
              "format": "qcow2",
              "location": "https://github.com/vibe-agi/hideout/releases/download/runtime-2026.07.0/developer-standard-2026.07.0-linux-aarch64.qcow2",
              "sha256": "%s",
              "downloadBytes": 1000000000,
              "virtualBytes": 12000000000,
              "supplyMode": "hideout-built",
              "source": {
                "baseLocation": "https://cloud.debian.org/images/cloud/trixie/20260706-2531/debian-13-genericcloud-arm64-20260706-2531.qcow2",
                "baseSHA512": "%s",
                "baseSHA256": "%s",
                "buildCommit": "0123456789ab",
                "sourceLockSHA256": "%s",
                "licenseReview": "reviewed"
              },
              "packageInventoryDigest": "sha256:%s",
              "sbom": {
                "available": false,
                "status": "unavailable-preview"
              }
            }
          ]
        }
      ]
    }
  ]
}`, digest, strings.Repeat("a", 64), strings.Repeat("b", 128), strings.Repeat("c", 64), strings.Repeat("d", 64), strings.Repeat("e", 64)))
}

func revisionMap(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	return doc["families"].([]any)[0].(map[string]any)["revisions"].([]any)[0].(map[string]any)
}

func artifactMap(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	return revisionMap(t, doc)["artifacts"].([]any)[0].(map[string]any)
}

func cloneJSON(t *testing.T, value any) any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
