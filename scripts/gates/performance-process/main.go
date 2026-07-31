package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	processRSSLimitBytes = 256 << 20
	tuiReadyLimitMS      = 2000.0
	maxDiagnosticBytes   = 64 << 10
	daemonReadyWait      = 15 * time.Second
	processPollInterval  = 20 * time.Millisecond
)

type memoryMetric struct {
	Unit            string   `json:"unit"`
	Samples         []uint64 `json:"samples"`
	P50             uint64   `json:"p50"`
	P95             uint64   `json:"p95"`
	Maximum         uint64   `json:"maximum"`
	ThresholdP95    uint64   `json:"thresholdP95"`
	ThresholdPassed bool     `json:"thresholdPassed"`
}

type evidence struct {
	Schema      string `json:"schema"`
	GeneratedAt string `json:"generatedAt"`
	Result      string `json:"result"`
	Methodology struct {
		Samples        int    `json:"samples"`
		SampleInterval string `json:"sampleInterval"`
		Percentile     string `json:"percentile"`
		RSSSource      string `json:"rssSource"`
	} `json:"methodology"`
	DaemonRSS memoryMetric `json:"daemonRSS"`
	TUIRSS    memoryMetric `json:"tuiRSS"`
	TUIReady  struct {
		Unit            string  `json:"unit"`
		Elapsed         float64 `json:"elapsed"`
		Threshold       float64 `json:"threshold"`
		ThresholdPassed bool    `json:"thresholdPassed"`
	} `json:"tuiReady"`
}

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	accepted := len(data)
	if buffer.buf.Len() >= maxDiagnosticBytes {
		return accepted, nil
	}
	remaining := maxDiagnosticBytes - buffer.buf.Len()
	if len(data) > remaining {
		data = data[:remaining]
	}
	_, _ = buffer.buf.Write(data)
	return accepted, nil
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func main() {
	var (
		hideout string
		store   string
		out     string
		samples int
	)
	flag.StringVar(&hideout, "hideout", "", "absolute candidate hideout binary")
	flag.StringVar(&store, "store", "", "absolute isolated store root")
	flag.StringVar(&out, "out", "", "absolute private JSON evidence output")
	flag.IntVar(&samples, "samples", 15, "RSS sample count")
	flag.Parse()

	if err := run(hideout, store, out, samples); err != nil {
		fmt.Fprintf(os.Stderr, "performance-process: %v\n", err)
		os.Exit(1)
	}
}

func run(hideout, store, out string, samples int) (resultErr error) {
	for label, path := range map[string]string{
		"--hideout": hideout,
		"--store":   store,
		"--out":     out,
	} {
		if path == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("%s must be an absolute path", label)
		}
	}
	if samples < 5 || samples > 100 {
		return errors.New("sample count must be between 5 and 100")
	}
	info, err := os.Stat(hideout)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return errors.New("candidate hideout binary is not executable")
	}
	if err := os.MkdirAll(store, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(store, 0o700); err != nil {
		return err
	}
	env := append(os.Environ(), "HIDEOUT_STORE_ROOT="+store)

	var daemonOutput synchronizedBuffer
	daemonCommand := exec.Command(hideout, "daemon", "start", "--ttl", "30m")
	daemonCommand.Env = env
	daemonCommand.Stdout = &daemonOutput
	daemonCommand.Stderr = &daemonOutput
	if err := daemonCommand.Start(); err != nil {
		return err
	}
	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- daemonCommand.Wait()
	}()
	defer func() {
		stop := exec.Command(hideout, "daemon", "stop")
		stop.Env = env
		_ = stop.Run()
		if daemonCommand.Process != nil {
			_ = daemonCommand.Process.Signal(syscall.SIGTERM)
			select {
			case <-daemonDone:
			case <-time.After(2 * time.Second):
				_ = daemonCommand.Process.Kill()
				select {
				case <-daemonDone:
				case <-time.After(time.Second):
				}
			}
		}
	}()

	socket := filepath.Join(store, "daemon", "hideoutd.sock")
	if err := waitForProcess(
		daemonReadyWait,
		func() bool {
			info, statErr := os.Stat(socket)
			return statErr == nil && info.Mode()&os.ModeSocket != 0
		},
		daemonDone,
	); err != nil {
		return fmt.Errorf(
			"daemon did not become ready: %w: %s",
			err,
			daemonOutput.String(),
		)
	}
	initCommand := exec.Command(
		hideout,
		"init",
		"--no-input",
		"--profile", "default",
		"--template", "dev",
		"--backend", "lima",
		"--network", "direct",
	)
	initCommand.Env = env
	if output, err := initCommand.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize console fixture: %w: %s", err, output)
	}

	var tuiOutput synchronizedBuffer
	tuiCommand := exec.Command(hideout, "tui")
	tuiCommand.Env = env
	tuiStarted := time.Now()
	terminal, err := pty.StartWithSize(
		tuiCommand,
		&pty.Winsize{Rows: 30, Cols: 120},
	)
	if err != nil {
		return err
	}
	tuiDone := make(chan error, 1)
	go func() {
		_, _ = io.Copy(&tuiOutput, terminal)
	}()
	go func() {
		tuiDone <- tuiCommand.Wait()
	}()
	defer func() {
		_, _ = terminal.Write([]byte("q"))
		if tuiCommand.Process != nil {
			select {
			case <-tuiDone:
			case <-time.After(time.Second):
				_ = tuiCommand.Process.Signal(os.Interrupt)
				select {
				case <-tuiDone:
				case <-time.After(time.Second):
					_ = tuiCommand.Process.Kill()
					select {
					case <-tuiDone:
					case <-time.After(time.Second):
					}
				}
			}
		}
		_ = terminal.Close()
	}()
	if err := waitForProcess(
		5*time.Second,
		func() bool {
			output := tuiOutput.String()
			return strings.Contains(output, "\x1b[?1049h") &&
				strings.Contains(output, "Hideout")
		},
		tuiDone,
	); err != nil {
		return fmt.Errorf(
			"TUI did not become ready: %w: %q",
			err,
			tuiOutput.String(),
		)
	}
	tuiReadyMS := float64(time.Since(tuiStarted).Nanoseconds()) / 1e6

	daemonSamples := make([]uint64, 0, samples)
	tuiSamples := make([]uint64, 0, samples)
	for index := 0; index < samples; index++ {
		daemonRSS, err := processRSSBytes(daemonCommand.Process.Pid)
		if err != nil {
			return fmt.Errorf("sample daemon RSS: %w", err)
		}
		tuiRSS, err := processRSSBytes(tuiCommand.Process.Pid)
		if err != nil {
			return fmt.Errorf("sample TUI RSS: %w", err)
		}
		daemonSamples = append(daemonSamples, daemonRSS)
		tuiSamples = append(tuiSamples, tuiRSS)
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := terminal.Write([]byte("q")); err != nil {
		return err
	}
	select {
	case err := <-tuiDone:
		if err != nil {
			return fmt.Errorf("TUI exit: %w", err)
		}
	case <-time.After(3 * time.Second):
		return errors.New("TUI did not exit after q")
	}
	_ = terminal.Close()
	tuiCommand.Process = nil

	stopCommand := exec.Command(hideout, "daemon", "stop")
	stopCommand.Env = env
	if output, err := stopCommand.CombinedOutput(); err != nil {
		return fmt.Errorf("stop daemon: %w: %s", err, output)
	}
	select {
	case err := <-daemonDone:
		if err != nil {
			return fmt.Errorf("daemon exit: %w", err)
		}
	case <-time.After(5 * time.Second):
		return errors.New("daemon did not exit after ordered stop")
	}
	daemonCommand.Process = nil

	result := evidence{
		Schema:      "hideout.release-performance-process/v1",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Result:      "passed",
		DaemonRSS:   summarizeMemory(daemonSamples),
		TUIRSS:      summarizeMemory(tuiSamples),
	}
	result.Methodology.Samples = samples
	result.Methodology.SampleInterval = "100ms"
	result.Methodology.Percentile = "nearest-rank-ceiling"
	result.Methodology.RSSSource = "host-ps-rss-kibibytes"
	result.TUIReady.Unit = "milliseconds"
	result.TUIReady.Elapsed = tuiReadyMS
	result.TUIReady.Threshold = tuiReadyLimitMS
	result.TUIReady.ThresholdPassed = tuiReadyMS <= tuiReadyLimitMS
	if !result.DaemonRSS.ThresholdPassed ||
		!result.TUIRSS.ThresholdPassed ||
		!result.TUIReady.ThresholdPassed {
		result.Result = "failed"
	}
	if result.Result != "passed" {
		return fmt.Errorf(
			"threshold failed: daemon-rss-p95=%d tui-rss-p95=%d tui-ready=%.3fms",
			result.DaemonRSS.P95,
			result.TUIRSS.P95,
			result.TUIReady.Elapsed,
		)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(
		out,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(result)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf(
		"performance-process: passed daemon-rss-p95=%dB tui-rss-p95=%dB tui-ready=%.3fms evidence=%s\n",
		result.DaemonRSS.P95,
		result.TUIRSS.P95,
		result.TUIReady.Elapsed,
		out,
	)
	return nil
}

func waitForProcess(
	limit time.Duration,
	ready func() bool,
	done chan error,
) error {
	if limit <= 0 || ready == nil || done == nil || cap(done) == 0 {
		return errors.New("process readiness configuration is invalid")
	}
	timeout := time.NewTimer(limit)
	defer timeout.Stop()
	ticker := time.NewTicker(processPollInterval)
	defer ticker.Stop()
	for {
		if ready() {
			return nil
		}
		select {
		case processErr := <-done:
			// Cleanup still owns the one Wait result. Put it back after
			// observing it so cleanup cannot wait for a consumed value.
			done <- processErr
			if processErr != nil {
				return fmt.Errorf(
					"process exited before readiness: %w",
					processErr,
				)
			}
			return errors.New("process exited before readiness")
		case <-ticker.C:
		case <-timeout.C:
			return errors.New("timed out")
		}
	}
}

func processRSSBytes(pid int) (uint64, error) {
	output, err := exec.Command(
		"/bin/ps",
		"-o", "rss=",
		"-p", strconv.Itoa(pid),
	).Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	kibibytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil || kibibytes == 0 {
		return 0, fmt.Errorf("invalid RSS %q", value)
	}
	if kibibytes > math.MaxUint64/1024 {
		return 0, errors.New("RSS overflow")
	}
	return kibibytes * 1024, nil
}

func summarizeMemory(samples []uint64) memoryMetric {
	values := append([]uint64(nil), samples...)
	sort.Slice(values, func(left, right int) bool {
		return values[left] < values[right]
	})
	p50 := nearestRank(values, 50)
	p95 := nearestRank(values, 95)
	return memoryMetric{
		Unit: "bytes", Samples: samples,
		P50: p50, P95: p95, Maximum: values[len(values)-1],
		ThresholdP95:    processRSSLimitBytes,
		ThresholdPassed: p95 <= processRSSLimitBytes,
	}
}

func nearestRank(sorted []uint64, percentile int) uint64 {
	index := int(math.Ceil(float64(len(sorted))*float64(percentile)/100.0)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
