package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

func TestProbeReportsPinnedZeroNetworkExecutorContract(t *testing.T) {
	var output bytes.Buffer
	if err := runCLIWithCapabilityProbe(
		[]string{"--probe"}, strings.NewReader(""), &output, func() error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	var probe vzexecutor.Probe
	decoder := json.NewDecoder(&output)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&probe); err != nil {
		t.Fatal(err)
	}
	if err := probe.Validate(); err != nil {
		t.Fatal(err)
	}
	if probe.NetworkDeviceCount != 0 || probe.ControlChannel != "virtiofs-private" {
		t.Fatalf("probe=%+v", probe)
	}
}

func TestProbeFailsClosedWhenVirtualizationCapabilityIsUnavailable(t *testing.T) {
	var output bytes.Buffer
	err := runCLIWithCapabilityProbe(
		[]string{"--probe"}, strings.NewReader(""), &output,
		func() error { return errors.New("private detail") },
	)
	if err == nil || strings.Contains(err.Error(), "private detail") || output.Len() != 0 {
		t.Fatalf("probe error=%v output=%q", err, output.String())
	}
}

func TestCLIRejectsArgumentsAndUnknownRequestFields(t *testing.T) {
	if err := runCLI([]string{"--root", "/tmp/disk"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("untyped execution arguments were accepted")
	}
	input := `{"schema":"hideout.migration-vz-adopt-request/v1","script":"curl example.com"}`
	if err := runCLI(nil, strings.NewReader(input), &bytes.Buffer{}); err == nil ||
		strings.Contains(err.Error(), "curl") {
		t.Fatalf("unknown-field error=%v", err)
	}
}

func TestCloudBoothookContainsOnlyFixedOfflineMountsAndHelperEntrypoint(t *testing.T) {
	script := cloudBoothook
	for _, required := range []string{
		"hideout-migration-request",
		"hideout-migration-helper",
		"hideout-migration-receipt",
		"/run/hideout-migration-helper/hideout-migration-adopt",
		"-t virtiofs",
		"poweroff -f",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("cloud boothook lacks %q: %s", required, script)
		}
	}
	for _, forbidden := range []string{"curl", "wget", "ssh", "http://", "https://", "$1", "eval"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("cloud boothook contains forbidden %q: %s", forbidden, script)
		}
	}
}
