//go:build !linux

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNonLinuxSupervisorFailsExplicitly(t *testing.T) {
	var output bytes.Buffer
	err := runSupervisor(strings.NewReader("ignored"), &output)
	if err == nil || !strings.Contains(err.Error(), "only supported on Linux") {
		t.Fatalf("error=%v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("unsupported platform wrote protocol output %q", output.String())
	}
}
