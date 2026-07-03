package audit

import (
	"encoding/json"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	urlSubstringRE            = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)
	hideoutSecretNameRE       = regexp.MustCompile(`(?i)\bHIDEOUT_SECRET_[A-Z0-9_]*\b`)
	hideoutSecretAssignmentRE = regexp.MustCompile(`(?i)\bHIDEOUT_SECRET_[A-Z0-9_]*=([^\s&;]+)`)
	secretAssignmentRE        = regexp.MustCompile(`(?i)\b(token|password|passwd|secret|api[_-]?key|access[_-]?key|authorization|cookie)=([^\s&;]+)`)
	secretFlagRE              = regexp.MustCompile(`(?i)(-{1,2}(?:token|password|passwd|secret|api[-_]?key|access[-_]?key|authorization|cookie)(?:=|\s+))([^\s]+)`)
	authHeaderRE              = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*(?:bearer|basic|digest|token)?\s*)[^\s,;]+`)
	cookieHeaderRE            = regexp.MustCompile(`(?i)\b((?:cookie|set-cookie)\s*[:=]\s*)[^\r\n]+`)
	apiKeyHeaderRE            = regexp.MustCompile(`(?i)\b((?:x[-_]?api[-_]?key|api[-_]?key|access[-_]?key|private[-_]?token|auth[-_]?token|x[-_]?auth[-_]?token)\s*[:=]\s*)[^\s,;]+`)
	machineIDValueRE          = regexp.MustCompile(`(?i)\b((?:guest[-_ ]?)?machine[-_ ]?id\s*(?:[:=]|\s+)\s*)[0-9a-f]{32}\b`)
)

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
	enc    *json.Encoder
	closer io.Closer
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
	return w.enc.Encode(e)
}

func (w *Writer) Close() error {
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
	if IsSecretValueKey(key) {
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
			if IsSecretValueKey(k) {
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
	for i := 0; i < len(argv); i++ {
		out[i] = RedactString(argv[i])
		if (isSecretFlag(argv[i]) || containsHideoutSecretName(argv[i])) && !strings.Contains(argv[i], "=") && i+1 < len(argv) {
			i++
			out[i] = "REDACTED"
		}
	}
	return out
}

func RedactString(s string) string {
	s = hideoutSecretAssignmentRE.ReplaceAllString(s, "HIDEOUT_*=REDACTED")
	s = hideoutSecretNameRE.ReplaceAllString(s, "HIDEOUT_*")
	s = authHeaderRE.ReplaceAllString(s, "${1}REDACTED")
	s = cookieHeaderRE.ReplaceAllString(s, "${1}REDACTED")
	s = apiKeyHeaderRE.ReplaceAllString(s, "${1}REDACTED")
	s = machineIDValueRE.ReplaceAllString(s, "${1}REDACTED")
	s = secretFlagRE.ReplaceAllString(s, "${1}REDACTED")
	s = secretAssignmentRE.ReplaceAllString(s, "$1=REDACTED")
	if !strings.Contains(s, "://") {
		return s
	}
	return urlSubstringRE.ReplaceAllStringFunc(s, redactURLString)
}

func redactURLString(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return s
	}
	u.User = nil
	q := u.Query()
	for key := range q {
		if IsSecretValueKey(key) || isSensitiveQueryKey(key) {
			q.Set(key, "REDACTED")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func isSecretFlag(value string) bool {
	if !strings.HasPrefix(value, "-") {
		return false
	}
	name := strings.TrimLeft(value, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		name = before
	}
	return IsSecretValueKey(name)
}

func containsHideoutSecretName(value string) bool {
	return hideoutSecretNameRE.MatchString(value)
}

func IsSecretValueKey(key string) bool {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "machineid") {
		return true
	}
	if strings.Contains(normalized, "secretref") || strings.HasSuffix(normalized, "ref") {
		return false
	}
	for _, marker := range []string{
		"token",
		"password",
		"passwd",
		"credential",
		"authorization",
		"cookie",
		"privatekey",
		"apikey",
		"accesskey",
		"secret",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func isSensitiveQueryKey(key string) bool {
	normalized := normalizeKey(key)
	for _, marker := range []string{"auth", "code", "key"} {
		if normalized == marker || strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
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
