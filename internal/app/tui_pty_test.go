package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	enterAlternateScreen = "\x1b[?1049h"
	exitAlternateScreen  = "\x1b[?1049l"
)

func TestTUIProgramEntersAndRestoresAlternateScreenWithKeyboardNavigation(t *testing.T) {
	output, final, err := exerciseTUIProgram(t, "jq", &tuiPTYFixtureModel{})
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
	model, ok := final.(*tuiPTYFixtureModel)
	if !ok {
		t.Fatalf("final model=%T, want *tuiPTYFixtureModel", final)
	}
	if model.selection != 1 {
		t.Fatalf("keyboard navigation selection=%d want 1", model.selection)
	}
}

func TestTUIProgramRestoresAlternateScreenAfterCtrlC(t *testing.T) {
	output, _, err := exerciseTUIProgram(t, "\x03", &tuiPTYFixtureModel{})
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
}

func TestTUIProgramRestoresAlternateScreenAfterPanic(t *testing.T) {
	output, _, err := exerciseTUIProgram(t, "p", &tuiPTYFixtureModel{panicOnP: true})
	if !errors.Is(err, tea.ErrProgramPanic) {
		t.Fatalf("panic error=%v, want %v", err, tea.ErrProgramPanic)
	}
	assertAlternateScreenRestored(t, output)
}

func TestTUIRejectsNonTTYWithOnceRecovery(t *testing.T) {
	var output bytes.Buffer
	a := app{
		stdin:  strings.NewReader(""),
		stdout: &output,
		stderr: &output,
		terminalInteractive: func() bool {
			return false
		},
	}
	err := a.tui(nil)
	if err == nil {
		t.Fatal("non-TTY interactive tui unexpectedly succeeded")
	}
	message := err.Error()
	if !strings.Contains(message, "interactive terminal") || !strings.Contains(message, "hideout tui --once") {
		t.Fatalf("non-TTY recovery is not actionable: %q", message)
	}
}

func TestTUIOnceIsPlainAndWorksWithoutTTY(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIDEOUT_STORE_ROOT", root)
	store := profile.Store{Root: root}
	if _, err := store.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	a := app{
		stdin:  strings.NewReader(""),
		stdout: &output,
		stderr: io.Discard,
		terminalInteractive: func() bool {
			return false
		},
	}
	if err := a.tui([]string{"--once"}); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("--once emitted terminal control sequences: %q", rendered)
	}
	for _, text := range []string{"Hideout", "DAEMONLESS", "read-only", "NEXT"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("--once output missing %q:\n%s", text, rendered)
		}
	}
}

type tuiPTYFixtureModel struct {
	selection int
	panicOnP  bool
}

func (*tuiPTYFixtureModel) Init() tea.Cmd {
	return nil
}

func (model *tuiPTYFixtureModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return model, nil
	}
	switch key.String() {
	case "j":
		model.selection++
	case "p":
		if model.panicOnP {
			panic("tui restoration fixture")
		}
	case "q", "ctrl+c":
		return model, tea.Quit
	}
	return model, nil
}

func (model *tuiPTYFixtureModel) View() tea.View {
	view := tea.NewView("Hideout PTY fixture")
	view.AltScreen = true
	return view
}

type tuiProgramResult struct {
	model tea.Model
	err   error
}

func exerciseTUIProgram(t *testing.T, input string, model tea.Model) (string, tea.Model, error) {
	t.Helper()
	terminal, replica, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	defer replica.Close()
	if err := pty.Setsize(replica, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		t.Fatal(err)
	}

	var output synchronizedBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, terminal)
		close(readDone)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan tuiProgramResult, 1)
	go func() {
		final, runErr := runTUIProgram(ctx, replica, replica, model)
		result <- tuiProgramResult{model: final, err: runErr}
	}()

	waitForTUIOutput(t, &output, enterAlternateScreen)
	if _, err := io.WriteString(terminal, input); err != nil {
		t.Fatal(err)
	}

	var completed tuiProgramResult
	select {
	case completed = <-result:
	case <-ctx.Done():
		t.Fatal("TUI program did not terminate")
	}
	_ = replica.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("PTY output reader did not terminate")
	}
	return output.String(), completed.model, completed.err
}

func waitForTUIOutput(t *testing.T, output *synchronizedBuffer, expected string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), expected) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("TUI output never contained %q: %q", expected, output.String())
}

func assertAlternateScreenRestored(t *testing.T, output string) {
	t.Helper()
	enter := strings.Index(output, enterAlternateScreen)
	exit := strings.LastIndex(output, exitAlternateScreen)
	if enter < 0 || exit < 0 || exit < enter {
		t.Fatalf("alternate screen was not entered and restored in order: %q", output)
	}
}

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(data)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
