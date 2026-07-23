//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

type sessionWire struct {
	reader              *sessionwire.Reader
	writer              *sessionwire.Writer
	sessionID           string
	terminal            sessionwire.TerminalDescriptor
	projectionReadiness *projectionReadinessSpec
	sessionRoot         string
}

func newSessionWire(reader io.Reader, writer io.Writer) *sessionWire {
	return &sessionWire{
		reader:      sessionwire.NewReader(reader, sessionwire.DaemonToSupervisor),
		writer:      sessionwire.NewWriter(writer, sessionwire.SupervisorToDaemon),
		sessionRoot: "/hideout/session",
	}
}

func (w *sessionWire) ReadStart() (startSpec, error) {
	frame, err := w.reader.ReadFrame()
	if err != nil {
		return startSpec{}, err
	}
	if frame.Type != sessionwire.TypeSupervisorStart {
		return startSpec{}, fmt.Errorf("first frame must be supervisor-start, got %s", frame.Type)
	}
	decoded, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
	if err != nil {
		return startSpec{}, err
	}
	start, ok := decoded.(*sessionwire.SupervisorStart)
	if !ok {
		return startSpec{}, errors.New("supervisor-start decoded to an unexpected control type")
	}
	envNames := make([]string, 0, len(start.Env))
	for name := range start.Env {
		envNames = append(envNames, name)
	}
	slices.Sort(envNames)
	env := make([]string, 0, len(envNames))
	for _, name := range envNames {
		env = append(env, name+"="+start.Env[name])
	}
	w.sessionID = start.SessionID
	w.terminal = start.Terminal
	if start.ProjectionReadiness != nil {
		w.projectionReadiness = &projectionReadinessSpec{
			EnvironmentID:     start.ProjectionReadiness.EnvironmentID,
			SessionSnapshotID: start.ProjectionReadiness.SessionSnapshotID,
			CatalogDigest:     start.ProjectionReadiness.CatalogDigest,
			ExpectedEntries:   start.ProjectionReadiness.ExpectedEntries,
			TargetProjected:   start.ProjectionReadiness.TargetProjected,
		}
	}
	return startSpec{
		Protocol:   start.Protocol,
		SessionID:  start.SessionID,
		TargetUser: start.TargetUser,
		GuestWork:  start.GuestWork,
		Argv:       append([]string(nil), start.Argv...),
		Env:        env,
		Terminal: terminalSpec{
			Mode:    string(start.Terminal.Mode),
			Rows:    start.Terminal.Rows,
			Columns: start.Terminal.Columns,
			Term:    start.Terminal.Term,
		},
		ExpectedBootID:      start.ExpectedBootID,
		SessionSource:       start.SessionSource,
		ProjectionReadiness: w.projectionReadiness,
	}, nil
}

func (w *sessionWire) ReadCommit() error {
	frame, err := w.reader.ReadFrame()
	if err != nil {
		return err
	}
	if frame.Type != sessionwire.TypeSupervisorCommit || len(frame.Payload) != 0 {
		return fmt.Errorf("supervisor ready must be followed by an empty commit frame, got %s", frame.Type)
	}
	return nil
}

func (w *sessionWire) ReadControl() (supervisorControl, error) {
	for {
		frame, err := w.reader.ReadFrame()
		if err != nil {
			return supervisorControl{}, err
		}
		if frame.Type.IsExtension() {
			continue
		}
		switch frame.Type {
		case sessionwire.TypeStdin:
			return supervisorControl{Kind: controlStdin, Data: frame.Payload}, nil
		case sessionwire.TypeStdinEOF:
			return supervisorControl{Kind: controlStdinEOF}, nil
		case sessionwire.TypeResize:
			control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if err != nil {
				return supervisorControl{}, err
			}
			resize, ok := control.(*sessionwire.Resize)
			if !ok {
				return supervisorControl{}, errors.New("resize decoded to an unexpected control type")
			}
			return supervisorControl{Kind: controlResize, Rows: resize.Rows, Columns: resize.Columns}, nil
		case sessionwire.TypeSignal:
			control, err := sessionwire.DecodeControl(frame.Type, frame.Payload)
			if err != nil {
				return supervisorControl{}, err
			}
			signal, ok := control.(*sessionwire.Signal)
			if !ok {
				return supervisorControl{}, errors.New("signal decoded to an unexpected control type")
			}
			return supervisorControl{Kind: controlSignal, Signal: signal.Name}, nil
		case sessionwire.TypeCancel:
			return supervisorControl{Kind: controlCancel}, nil
		case sessionwire.TypeHeartbeat:
			return supervisorControl{Kind: controlHeartbeat}, nil
		default:
			return supervisorControl{}, fmt.Errorf("unexpected daemon control %s", frame.Type)
		}
	}
}

func (w *sessionWire) WriteReady() error {
	ready := &sessionwire.SupervisorReady{
		Protocol:  sessionwire.SupervisorProtocol,
		SessionID: w.sessionID,
		Terminal:  w.terminal,
	}
	if w.projectionReadiness != nil {
		observation, err := observeProjectionReadiness(w.sessionRoot, w.sessionID, *w.projectionReadiness)
		if err != nil {
			reason := readinessReason(err)
			if reason == "" {
				reason = string(backend.ProjectionReadinessEntryInvalid)
			}
			_ = w.WriteError(reason, boundedSummary(err))
			return err
		}
		ready.ProjectionReadiness = &sessionwire.SupervisorProjectionReadinessReady{
			Status:            "ready",
			EnvironmentID:     w.projectionReadiness.EnvironmentID,
			SessionSnapshotID: w.projectionReadiness.SessionSnapshotID,
			CatalogDigest:     observation.CatalogDigest,
			ExpectedEntries:   observation.ExpectedEntries,
			ObservedEntries:   observation.ObservedEntries,
			DurationMillis:    observation.DurationMillis,
			TargetProjected:   observation.TargetProjected,
		}
	}
	return w.writer.WriteControl(sessionwire.TypeSupervisorReady, ready)
}

func (w *sessionWire) WriteOutput(kind outputKind, payload []byte) error {
	var frameType sessionwire.Type
	switch kind {
	case outputTerminal:
		frameType = sessionwire.TypeTerminal
	case outputStdout:
		frameType = sessionwire.TypeStdout
	case outputStderr:
		frameType = sessionwire.TypeStderr
	default:
		return fmt.Errorf("unsupported supervisor output kind %d", kind)
	}
	return w.writer.Write(frameType, payload)
}

func (w *sessionWire) WriteError(code, summary string) error {
	if summary == "" {
		summary = "guest session supervisor failed"
	}
	return w.writer.WriteControl(sessionwire.TypeSupervisorError, &sessionwire.Error{
		Code:    code,
		Summary: summary,
	})
}

func (w *sessionWire) WriteCompletion(completion targetCompletion) error {
	return w.writer.WriteControl(sessionwire.TypeCompletion, &sessionwire.Completion{
		Kind:             sessionwire.CompletionKind(completion.Kind),
		ExitCode:         completion.ExitCode,
		Signal:           completion.Signal,
		TargetCompleted:  completion.Completed,
		CleanupCompleted: false,
		SessionID:        w.sessionID,
		Summary:          "",
	})
}
