package packagekit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type AuditEvent struct {
	Schema        string `json:"schema"`
	Time          string `json:"time"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	Prefix        string `json:"prefix,omitempty"`
	StoreRoot     string `json:"storeRoot,omitempty"`
	Files         int    `json:"files,omitempty"`
	StaleFiles    int    `json:"staleFiles,omitempty"`
	RepairRemoved int    `json:"repairRemoved,omitempty"`
	DurableAction string `json:"durableAction,omitempty"`
	Purge         bool   `json:"purge,omitempty"`
	SurvivorAudit string `json:"survivorAudit,omitempty"`
}

func writeAudit(storeRoot string, event AuditEvent) {
	if storeRoot == "" {
		return
	}
	logDir := filepath.Join(storeRoot, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return
	}
	writeAuditFile(filepath.Join(logDir, "package-audit.jsonl"), event)
}

func writePurgeAudit(storeRoot string, event AuditEvent) {
	if storeRoot == "" {
		return
	}
	parent := filepath.Dir(filepath.Clean(storeRoot))
	if parent == "" || parent == "." {
		return
	}
	path := filepath.Join(parent, "hideout-package-purge-audit.jsonl")
	event.SurvivorAudit = path
	writeAuditFile(path, event)
}

func writeAuditFile(path string, event AuditEvent) {
	event.Schema = "hideout.package-audit.v1"
	if event.Time == "" {
		event.Time = time.Now().UTC().Format(time.RFC3339)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(event)
}
