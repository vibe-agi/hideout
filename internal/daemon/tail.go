package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
)

// startAuditTail launches a goroutine that tails each session audit.jsonl and
// republishes newly appended records as redacted audit events. It skips each
// file's backlog on first sight, so a (re)start replays no history — the client
// seeds current state with one overview read and then consumes new events.
func (d *Daemon) startAuditTail() {
	d.tailStop = make(chan struct{})
	go d.tailLoop(filepath.Join(d.store.Root, "sessions"))
}

func (d *Daemon) tailLoop(sessionsDir string) {
	seen := map[string]int{}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-d.tailStop:
			return
		case <-ticker.C:
			entries, err := os.ReadDir(sessionsDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				d.tailFile(filepath.Join(sessionsDir, e.Name(), "audit.jsonl"), seen)
			}
		}
	}
}

func (d *Daemon) tailFile(path string, seen map[string]int) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	var lines [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		b := append([]byte(nil), sc.Bytes()...)
		if len(b) > 0 {
			lines = append(lines, b)
		}
	}
	start, known := seen[path]
	if !known {
		// First sight: skip the existing backlog so a restart replays no history.
		seen[path] = len(lines)
		return
	}
	for i := start; i < len(lines); i++ {
		var ev audit.Event
		if json.Unmarshal(lines[i], &ev) != nil {
			continue
		}
		d.bus.publishAuditEvent(ev)
	}
	seen[path] = len(lines)
}
