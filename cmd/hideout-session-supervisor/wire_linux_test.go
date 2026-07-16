//go:build linux

package main

import (
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/vibe-agi/hideout/internal/sessionwire"
)

func TestSessionWireRequiresStartAndPreservesBinaryControls(t *testing.T) {
	var input bytes.Buffer
	writer := sessionwire.NewWriter(&input, sessionwire.DaemonToSupervisor)
	start := &sessionwire.SupervisorStart{
		Protocol:       sessionwire.SupervisorProtocol,
		SessionID:      "ses_20260716T120000Z_0123456789abcdef",
		TargetUser:     "developer",
		GuestWork:      "/workspace",
		Argv:           []string{"sh"},
		Env:            map[string]string{"Z": "last", "A": "first"},
		Terminal:       sessionwire.TerminalDescriptor{Mode: sessionwire.TerminalPTY, Rows: 24, Columns: 80, Term: "xterm-256color"},
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		SessionSource:  "/hideout/runtime/sessions/ses_20260716T120000Z_0123456789abcdef",
	}
	if err := writer.WriteControl(sessionwire.TypeSupervisorStart, start); err != nil {
		t.Fatal(err)
	}
	binaryInput := []byte{0, 1, '\n', 0xff, 0x1b}
	if err := writer.Write(sessionwire.TypeStdin, binaryInput); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteControl(sessionwire.TypeResize, &sessionwire.Resize{Rows: 40, Columns: 120}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteControl(sessionwire.TypeSignal, &sessionwire.Signal{Name: "SIGTERM"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(sessionwire.TypeHeartbeat, nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(sessionwire.TypeCancel, nil); err != nil {
		t.Fatal(err)
	}

	wire := newSessionWire(&input, io.Discard)
	gotStart, err := wire.ReadStart()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotStart.Env, []string{"A=first", "Z=last"}) {
		t.Fatalf("environment=%q", gotStart.Env)
	}
	want := []supervisorControl{
		{Kind: controlStdin, Data: binaryInput},
		{Kind: controlResize, Rows: 40, Columns: 120},
		{Kind: controlSignal, Signal: "SIGTERM"},
		{Kind: controlHeartbeat},
		{Kind: controlCancel},
	}
	for index := range want {
		got, readErr := wire.ReadControl()
		if readErr != nil {
			t.Fatalf("control %d: %v", index, readErr)
		}
		if !reflect.DeepEqual(got, want[index]) {
			t.Fatalf("control %d=%+v want %+v", index, got, want[index])
		}
	}
}

func TestSessionWireWritesReadySeparatedDataAndTypedCompletion(t *testing.T) {
	var input bytes.Buffer
	inputWriter := sessionwire.NewWriter(&input, sessionwire.DaemonToSupervisor)
	start := &sessionwire.SupervisorStart{
		Protocol:       sessionwire.SupervisorProtocol,
		SessionID:      "ses_20260716T120000Z_0123456789abcdef",
		TargetUser:     "developer",
		GuestWork:      "/workspace",
		Argv:           []string{"true"},
		Env:            map[string]string{"PATH": "/usr/bin:/bin"},
		Terminal:       sessionwire.TerminalDescriptor{Mode: sessionwire.TerminalNone},
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		SessionSource:  "/hideout/runtime/sessions/ses_20260716T120000Z_0123456789abcdef",
	}
	if err := inputWriter.WriteControl(sessionwire.TypeSupervisorStart, start); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	wire := newSessionWire(&input, &output)
	if _, err := wire.ReadStart(); err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteReady(); err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteOutput(outputStdout, []byte("out\x00")); err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteOutput(outputStderr, []byte("err\n")); err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteCompletion(targetCompletion{Kind: "exit", ExitCode: 7, Completed: true}); err != nil {
		t.Fatal(err)
	}

	reader := sessionwire.NewReader(&output, sessionwire.SupervisorToDaemon)
	for index, wantType := range []sessionwire.Type{sessionwire.TypeSupervisorReady, sessionwire.TypeStdout, sessionwire.TypeStderr, sessionwire.TypeCompletion} {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
		if frame.Type != wantType {
			t.Fatalf("frame %d type=%s want %s", index, frame.Type, wantType)
		}
	}
}

func TestSessionWireRejectsControlBeforeStart(t *testing.T) {
	var input bytes.Buffer
	writer := sessionwire.NewWriter(&input, sessionwire.DaemonToSupervisor)
	if err := writer.Write(sessionwire.TypeHeartbeat, nil); err != nil {
		t.Fatal(err)
	}
	wire := newSessionWire(&input, io.Discard)
	if _, err := wire.ReadStart(); err == nil {
		t.Fatal("expected first-frame failure")
	}
}
