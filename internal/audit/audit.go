package audit

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Redaction is deterministic, not heuristic. Core strips only what it minted
// and can name exactly: the HIDEOUT_SECRET_* backing namespace, Core-minted
// control-plane token values (cap_/ui_ + hex), Core's own control-plane detail
// field names, and generated machine-id identity material. User/application
// request data (URLs, argv, query values, headers, paths) is host-local
// evidence and is recorded verbatim; Core does not guess which user values are
// secrets. Redacting user data belongs to user-owned audit.redact policy and
// to the export/share boundary. See docs/privacy-run-design.md (Audit and
// Explain) and docs/threat-model.md (Evidence Requirements).
var (
	hideoutSecretNameRE = regexp.MustCompile(`(?i)\bHIDEOUT_SECRET_[A-Z0-9_]*\b`)
	// Backing values adjacent to a self-known HIDEOUT_SECRET_* name are
	// stripped in KEY=value, KEY: value, and JSON "KEY":"value" forms. In
	// prose this can eat the single token following "KEY:"; that over-eat is
	// deliberate — conservative for a self-known secret namespace.
	hideoutSecretAssignmentRE = regexp.MustCompile(`(?i)\bHIDEOUT_SECRET_[A-Z0-9_]*\s*[:=]\s*[^\s&;]+`)
	hideoutSecretJSONRE       = regexp.MustCompile(`(?i)"HIDEOUT_SECRET_[A-Z0-9_]*"\s*:\s*"[^"]*"`)
	controlPlaneTokenRE       = regexp.MustCompile(`\b(?:cap|ui)_[0-9a-f]{16,}\b`)
	machineIDValueRE          = regexp.MustCompile(`(?i)\b((?:guest[-_ ]?)?machine[-_ ]?id\s*(?:[:=]|\s+)\s*)[0-9a-f]{32}\b`)
)

// controlPlaneKeys are Core's own control-plane detail field names. Matching
// these exactly is self-knowledge, not heuristic guessing: they are field
// names Hideout itself emits, never user business fields.
var controlPlaneKeys = map[string]bool{
	"capabilitytoken": true,
	"brokertoken":     true,
	"uitoken":         true,
	"managertoken":    true,
}

type Event struct {
	Time     time.Time      `json:"time"`
	Session  string         `json:"session"`
	Profile  string         `json:"profile"`
	Backend  string         `json:"backend"`
	Action   string         `json:"action"`
	Decision string         `json:"decision"`
	Details  map[string]any `json:"details,omitempty"`
}

type Writer struct {
	mu     sync.Mutex
	enc    *json.Encoder
	closer io.Closer
	closed bool
}

func NewFile(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Writer{enc: json.NewEncoder(f), closer: f}, nil
}

func NewDiscard() *Writer {
	return &Writer{enc: json.NewEncoder(io.Discard)}
}

func (w *Writer) Emit(e Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.Details = RedactDetails(e.Details)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("audit writer is closed")
	}
	return w.enc.Encode(e)
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.closer == nil {
		return nil
	}
	return w.closer.Close()
}

func RedactDetails(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[RedactKey(k)] = RedactValue(k, v)
	}
	return out
}

func RedactKey(key string) string {
	return hideoutSecretNameRE.ReplaceAllString(key, "HIDEOUT_*")
}

func RedactValue(key string, value any) any {
	if isControlPlaneKey(key) {
		return "REDACTED"
	}
	switch v := value.(type) {
	case string:
		return RedactString(v)
	case []string:
		return RedactArgv(v)
	case []any:
		stringsOnly := make([]string, 0, len(v))
		allStrings := true
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				allStrings = false
				break
			}
			stringsOnly = append(stringsOnly, s)
		}
		if allStrings {
			redacted := RedactArgv(stringsOnly)
			out := make([]any, len(redacted))
			for i, item := range redacted {
				out[i] = item
			}
			return out
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = RedactValue("", item)
		}
		return out
	case map[string]any:
		return RedactDetails(v)
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, item := range v {
			if isControlPlaneKey(k) {
				out[RedactKey(k)] = "REDACTED"
			} else {
				out[RedactKey(k)] = RedactString(item)
			}
		}
		return out
	default:
		return value
	}
}

func RedactArgv(argv []string) []string {
	out := make([]string, len(argv))
	for i := range argv {
		out[i] = RedactString(argv[i])
	}
	return out
}

// RedactString strips only Hideout-minted control-plane material: the
// HIDEOUT_SECRET_* backing namespace, Core-minted token values, and generated
// machine-id identity material. All other text is user/application data and is
// returned verbatim.
func RedactString(s string) string {
	s = hideoutSecretJSONRE.ReplaceAllString(s, `"HIDEOUT_*":"REDACTED"`)
	s = hideoutSecretAssignmentRE.ReplaceAllString(s, "HIDEOUT_*=REDACTED")
	s = hideoutSecretNameRE.ReplaceAllString(s, "HIDEOUT_*")
	s = controlPlaneTokenRE.ReplaceAllString(s, "REDACTED")
	s = machineIDValueRE.ReplaceAllString(s, "${1}REDACTED")
	return s
}

// isControlPlaneKey reports whether key is one of Core's own control-plane
// detail field names or one of Core's own machine-id field names (machineId /
// guestMachineId and separator variants, which normalizeKey collapses). All
// are self-known: Core emits these field names itself, so an exact match is
// not a heuristic guess about user data. Fields that merely contain a
// machine-id-like substring (e.g. customerMachineId) are user data.
func isControlPlaneKey(key string) bool {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false
	}
	if normalized == "machineid" || normalized == "guestmachineid" {
		return true
	}
	if strings.HasPrefix(normalized, "hideoutsecret") {
		return true
	}
	return controlPlaneKeys[normalized]
}

func normalizeKey(key string) string {
	key = strings.ToLower(key)
	var b strings.Builder
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
