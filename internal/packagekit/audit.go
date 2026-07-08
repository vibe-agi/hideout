package packagekit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type AuditEvent struct {
	Schema    string `json:"schema"`
	Time      string `json:"time"`
	Operation string `json:"operation"`
	Status    string `json:"status"`
	Prefix    string `json:"prefix,omitempty"`
	StoreRoot string `json:"storeRoot,omitempty"`
	Files     int    `json:"files,omitempty"`
	Purge     bool   `json:"purge,omitempty"`
}

func writeAudit(storeRoot string, event AuditEvent) {
	if storeRoot == "" {
		return
	}
	logDir := filepath.Join(storeRoot, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return
	}
	event.Schema = "hideout.package-audit.v1"
	if event.Time == "" {
		event.Time = time.Now().UTC().Format(time.RFC3339)
	}
	path := filepath.Join(logDir, "package-audit.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(event)
}
