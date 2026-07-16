package main

import "errors"

type controlKind uint8

const (
	controlStdin controlKind = iota + 1
	controlStdinEOF
	controlResize
	controlSignal
	controlCancel
	controlHeartbeat
)

type supervisorControl struct {
	Kind    controlKind
	Data    []byte
	Rows    uint16
	Columns uint16
	Signal  string
	Reason  string
}

type outputKind uint8

const (
	outputTerminal outputKind = iota + 1
	outputStdout
	outputStderr
)

var errOutputBackpressure = errors.New("supervisor output queue exceeded its bound")

type supervisorWire interface {
	ReadStart() (startSpec, error)
	ReadControl() (supervisorControl, error)
	WriteReady() error
	WriteOutput(outputKind, []byte) error
	WriteError(code, summary string) error
	WriteCompletion(targetCompletion) error
}
