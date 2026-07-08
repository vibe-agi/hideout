package export

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/doctor"
)

type sourceData struct {
	Body        any
	RecordCount int
	Notice      string
}

func readSource(req Request) (sourceData, error) {
	switch req.Source {
	case SourceAudit:
		events := make([]AuditEvent, len(req.AuditEvents))
		copy(events, req.AuditEvents)
		notice := ""
		if len(events) == 0 {
			notice = "0 records matched"
		}
		return sourceData{Body: events, RecordCount: len(events), Notice: notice}, nil
	case SourceBundle:
		return readBundleSource(req.BundlePath)
	case SourceBoundarySummary:
		return readBoundarySummarySource(req.BoundarySummary, req.BoundaryAuditPath)
	case SourceDoctorReport:
		return readDoctorReportSource(req.DoctorReportPath)
	default:
		return sourceData{}, fmt.Errorf("unsupported export source %q", req.Source)
	}
}

func readDoctorReportSource(path string) (sourceData, error) {
	if path == "" {
		return sourceData{}, errors.New("doctor report path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceData{}, fmt.Errorf("read doctor report: %w", err)
	}
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		return sourceData{}, fmt.Errorf("parse doctor report: %w", err)
	}
	if schema, _ := report["schema"].(string); schema != doctor.Schema {
		return sourceData{}, fmt.Errorf("doctor report schema %q is not supported", schema)
	}
	count := 1
	if findings, ok := report["findings"].([]any); ok {
		count = len(findings)
	}
	return sourceData{Body: report, RecordCount: count}, nil
}

func readBundleSource(path string) (sourceData, error) {
	if path == "" {
		return sourceData{}, errors.New("bundle path is required")
	}
	manifestPath := path
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		manifestPath = filepath.Join(path, "manifest.json")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return sourceData{}, fmt.Errorf("read bundle manifest: %w", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		return sourceData{}, fmt.Errorf("parse bundle manifest: %w", err)
	}
	var evidenceLogPath string
	if evidence, ok := manifest["evidence"].(map[string]any); ok {
		dir, _ := evidence["directory"].(string)
		logName, _ := evidence["log"].(string)
		if logName != "" {
			logPath := resolveBundleReference(manifestPath, dir, logName)
			logData, err := os.ReadFile(logPath)
			if err != nil {
				return sourceData{}, fmt.Errorf("resolve bundle evidence log: %w", err)
			}
			evidenceLogPath = logPath
			evidence["logContent"] = string(logData)
			evidence["log"] = "inline:evidence.logContent"
		}
		delete(evidence, "directory")
	}
	if gates, ok := manifest["isolationGates"].([]any); ok {
		for _, raw := range gates {
			gate, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			auditPath, _ := gate["auditPath"].(string)
			if auditPath == "" || auditPath == "off" {
				delete(gate, "auditPath")
				continue
			}
			resolvedPath := resolveBundleReference(manifestPath, "", auditPath)
			if samePath(resolvedPath, evidenceLogPath) {
				gate["auditContentRef"] = "inline:evidence.logContent"
				delete(gate, "auditPath")
				continue
			}
			auditData, err := os.ReadFile(resolvedPath)
			if err != nil {
				return sourceData{}, fmt.Errorf("resolve bundle isolation gate auditPath: %w", err)
			}
			gate["auditContent"] = string(auditData)
			delete(gate, "auditPath")
		}
	}
	count := 1
	if gates, ok := manifest["isolationGates"].([]any); ok {
		count = len(gates)
	} else if gates, ok := manifest["gates"].([]any); ok {
		count = len(gates)
	}
	return sourceData{Body: manifest, RecordCount: count}, nil
}

func resolveBundleReference(manifestPath, evidenceDir, ref string) string {
	if filepath.IsAbs(ref) {
		return ref
	}
	base := filepath.Dir(manifestPath)
	if evidenceDir != "" {
		if filepath.IsAbs(evidenceDir) {
			base = evidenceDir
		} else {
			base = filepath.Join(base, evidenceDir)
		}
	}
	return filepath.Join(base, ref)
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if abs, err := filepath.Abs(a); err == nil {
		a = abs
	}
	if abs, err := filepath.Abs(b); err == nil {
		b = abs
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func readBoundarySummarySource(summary any, auditPath string) (sourceData, error) {
	if summary == nil {
		return sourceData{}, errors.New("boundary summary is required")
	}
	body, err := normalizeMap(summary)
	if err != nil {
		return sourceData{}, fmt.Errorf("read boundary summary: %w", err)
	}
	if auditPath == "" {
		if value, _ := body["auditPath"].(string); value != "" {
			auditPath = value
		}
	}
	if auditPath != "" && auditPath != "off" {
		events, err := readAuditJSONL(auditPath)
		if err != nil {
			return sourceData{}, fmt.Errorf("resolve boundary auditPath: %w", err)
		}
		body["audit"] = events
	}
	delete(body, "auditPath")
	count := 1
	if events, ok := body["audit"].([]any); ok {
		count = len(events)
	}
	return sourceData{Body: body, RecordCount: count}, nil
}

func readAuditJSONL(path string) ([]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func normalizeMap(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
