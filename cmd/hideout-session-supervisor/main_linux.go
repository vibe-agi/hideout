//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/sessionwire"
)

type controlResult struct {
	control supervisorControl
	err     error
}

func runSupervisor(reader io.Reader, writer io.Writer) error {
	wire := newSessionWire(reader, writer)
	return runSupervisorWire(wire, verifySupervisorContext, startTarget, superviseTarget)
}

type supervisorContextVerifier func(startSpec) error
type supervisorTargetStarter func(startSpec, supervisorWire) (*targetProcess, error)
type supervisorTargetRunner func(*targetProcess, supervisorWire) error

func runSupervisorWire(
	wire supervisorWire,
	verify supervisorContextVerifier,
	start supervisorTargetStarter,
	run supervisorTargetRunner,
) error {
	spec, err := wire.ReadStart()
	if err != nil {
		return writeInitialFailure(wire, "supervisor.start.invalid", err)
	}
	if err := validateStart(spec, sessionwire.SupervisorProtocol); err != nil {
		return writeInitialFailure(wire, "supervisor.start.invalid", err)
	}
	if err := verify(spec); err != nil {
		return writeInitialFailure(wire, "supervisor.context.invalid", err)
	}

	if err := wire.WriteReady(); err != nil {
		return fmt.Errorf("write supervisor ready: %w", err)
	}
	if err := wire.ReadCommit(); err != nil {
		return fmt.Errorf("read supervisor commit: %w", err)
	}
	process, err := start(spec, wire)
	if err != nil {
		return writeInitialFailure(wire, "supervisor.target.start-failed", err)
	}
	process.queue.begin()
	return run(process, wire)
}

func writeInitialFailure(wire supervisorWire, code string, failure error) error {
	if writeErr := wire.WriteError(code, boundedSummary(failure)); writeErr != nil {
		return errors.Join(failure, writeErr)
	}
	return failure
}

func verifySupervisorContext(spec startSpec) error {
	if os.Geteuid() != 0 {
		return errors.New("supervisor must run under the fixed privileged launcher")
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return fmt.Errorf("read guest boot identity: %w", err)
	}
	if strings.TrimSpace(string(bootID)) != spec.ExpectedBootID {
		return errors.New("guest boot identity changed before target start")
	}
	view, err := os.Stat("/hideout/session")
	if err != nil {
		return fmt.Errorf("inspect fixed session view: %w", err)
	}
	if !view.IsDir() {
		return errors.New("fixed session view is not a directory")
	}
	return nil
}

func superviseTarget(process *targetProcess, wire supervisorWire) error {
	controls := make(chan controlResult, 1)
	go readControls(wire, controls)
	heartbeat := time.NewTimer(supervisorHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case result := <-process.wait:
			if outputErr := process.finishOutput(); outputErr != nil {
				return finishProtocolFailure(wire, result, "supervisor.output.failed", outputErr)
			}
			return wire.WriteCompletion(result.completion)
		case outputErr := <-process.queue.failure():
			result := process.terminateAndWait()
			if drainErr := process.finishOutput(); drainErr != nil && !errors.Is(drainErr, outputErr) {
				outputErr = errors.Join(outputErr, drainErr)
			}
			return finishProtocolFailure(wire, result, "supervisor.output.failed", outputErr)
		case incoming := <-controls:
			if incoming.err != nil {
				result := process.terminateAndWait()
				_ = process.finishOutput()
				if errors.Is(incoming.err, io.EOF) {
					if !result.completion.Completed {
						return result.err
					}
					return nil
				}
				return finishProtocolFailure(wire, result, "supervisor.protocol.invalid", incoming.err)
			}
			resetTimer(heartbeat, supervisorHeartbeat)
			if done, err := applyControl(process, incoming.control); done {
				result := process.terminateAndWait()
				if outputErr := process.finishOutput(); outputErr != nil {
					return outputErr
				}
				result.completion = targetCompletion{Kind: "cancelled", ExitCode: 130, Completed: result.completion.Completed}
				return wire.WriteCompletion(result.completion)
			} else if err != nil {
				result := process.terminateAndWait()
				_ = process.finishOutput()
				return finishProtocolFailure(wire, result, "supervisor.control.invalid", err)
			}
		case <-heartbeat.C:
			result := process.terminateAndWait()
			_ = process.finishOutput()
			return finishProtocolFailure(wire, result, "supervisor.heartbeat.expired", errors.New("daemon heartbeat expired"))
		}
	}
}

func readControls(wire supervisorWire, controls chan<- controlResult) {
	for {
		control, err := wire.ReadControl()
		controls <- controlResult{control: control, err: err}
		if err != nil {
			return
		}
	}
}

func applyControl(process *targetProcess, control supervisorControl) (bool, error) {
	switch control.Kind {
	case controlStdin:
		return false, process.writeStdin(control.Data)
	case controlStdinEOF:
		return false, process.closeInput()
	case controlResize:
		return false, process.resize(control.Rows, control.Columns)
	case controlSignal:
		signal, err := signalNumber(control.Signal)
		if err != nil {
			return false, err
		}
		return false, process.signal(signal)
	case controlCancel:
		return true, nil
	case controlHeartbeat:
		return false, nil
	default:
		return false, fmt.Errorf("unsupported supervisor control %d", control.Kind)
	}
}

func finishProtocolFailure(wire supervisorWire, result waitResult, code string, failure error) error {
	if err := wire.WriteError(code, boundedSummary(failure)); err != nil {
		return errors.Join(failure, err)
	}
	completion := targetCompletion{Kind: "protocol-error", ExitCode: 125, Completed: result.completion.Completed}
	if err := wire.WriteCompletion(completion); err != nil {
		return errors.Join(failure, err)
	}
	return failure
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}
