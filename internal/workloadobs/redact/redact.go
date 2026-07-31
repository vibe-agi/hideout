package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	Replacement = "[REDACTED]"

	defaultMaxValueBytes  = 8192
	defaultMaxOutputBytes = 512 << 10
	defaultMaxArguments   = 1024
)

var (
	ErrInvalidConfig   = errors.New("activity redaction configuration is invalid")
	ErrRedactionFailed = errors.New("activity redaction failed closed")

	urlPattern                 = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
	authPattern                = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:(?:basic|bearer)\s+)?([^\s,;]+)`)
	sensitiveAssignmentPattern = regexp.MustCompile(`(?i)(^|[?&;,\s])((?:access[_-]?token|api[_-]?key|password|passwd|secret|authorization|auth|credential|signature|sig))=([^&;,\s]+)`)
	controlTokenPattern        = regexp.MustCompile(`(?i)\b(?:cap|token|credential)_[A-Za-z0-9_-]{16,}\b`)
)

var sensitiveFlags = map[string]struct{}{
	"api-key":       {},
	"apikey":        {},
	"access-token":  {},
	"token":         {},
	"password":      {},
	"passwd":        {},
	"secret":        {},
	"authorization": {},
	"auth":          {},
	"credential":    {},
	"client-secret": {},
	"private-key":   {},
}

type Config struct {
	KnownSecrets   [][]byte
	ControlTokens  []string
	MaxValueBytes  int
	MaxOutputBytes int
	MaxArguments   int
}

type Redactor struct {
	knownSecrets   [][]byte
	controlTokens  []string
	maxValueBytes  int
	maxOutputBytes int
	maxArguments   int
}

func New(config Config) (*Redactor, error) {
	if config.MaxValueBytes < 0 || config.MaxOutputBytes < 0 || config.MaxArguments < 0 {
		return nil, ErrInvalidConfig
	}
	redactor := &Redactor{
		maxValueBytes:  config.MaxValueBytes,
		maxOutputBytes: config.MaxOutputBytes,
		maxArguments:   config.MaxArguments,
	}
	if redactor.maxValueBytes == 0 {
		redactor.maxValueBytes = defaultMaxValueBytes
	}
	if redactor.maxOutputBytes == 0 {
		redactor.maxOutputBytes = defaultMaxOutputBytes
	}
	if redactor.maxArguments == 0 {
		redactor.maxArguments = defaultMaxArguments
	}
	for _, secret := range config.KnownSecrets {
		if len(secret) == 0 || len(secret) > defaultMaxValueBytes*2 || !utf8.Valid(secret) || string(secret) == Replacement {
			redactor.Clear()
			return nil, ErrInvalidConfig
		}
		for _, variant := range protectedEncodingVariants(secret) {
			redactor.knownSecrets = appendUniqueBytes(
				redactor.knownSecrets,
				variant,
			)
		}
	}
	for _, token := range config.ControlTokens {
		if token == "" || len(token) > defaultMaxValueBytes*2 || !utf8.ValidString(token) || token == Replacement {
			redactor.Clear()
			return nil, ErrInvalidConfig
		}
		for _, variant := range protectedEncodingVariants([]byte(token)) {
			redactor.controlTokens = appendUniqueString(
				redactor.controlTokens,
				string(variant),
			)
		}
	}
	sort.Slice(redactor.knownSecrets, func(left, right int) bool {
		if len(redactor.knownSecrets[left]) == len(redactor.knownSecrets[right]) {
			return bytes.Compare(redactor.knownSecrets[left], redactor.knownSecrets[right]) < 0
		}
		return len(redactor.knownSecrets[left]) > len(redactor.knownSecrets[right])
	})
	sort.Slice(redactor.controlTokens, func(left, right int) bool {
		if len(redactor.controlTokens[left]) == len(redactor.controlTokens[right]) {
			return redactor.controlTokens[left] < redactor.controlTokens[right]
		}
		return len(redactor.controlTokens[left]) > len(redactor.controlTokens[right])
	})
	return redactor, nil
}

func (redactor *Redactor) Clear() {
	if redactor == nil {
		return
	}
	for index := range redactor.knownSecrets {
		clear(redactor.knownSecrets[index])
	}
	clear(redactor.knownSecrets)
	for index := range redactor.controlTokens {
		redactor.controlTokens[index] = ""
	}
	clear(redactor.controlTokens)
}

func (redactor *Redactor) Argv(arguments []string) ([]string, []string, error) {
	return redactor.argv(arguments, false)
}

func (redactor *Redactor) argv(
	arguments []string,
	incompleteURLPossible bool,
) ([]string, []string, error) {
	if redactor == nil || len(arguments) > redactor.maxArguments {
		return nil, nil, ErrRedactionFailed
	}
	output := make([]string, len(arguments))
	var truncation []string
	redactNext := false
	for index, argument := range arguments {
		if !utf8.ValidString(argument) {
			return nil, nil, ErrRedactionFailed
		}
		if redactNext {
			output[index] = Replacement
			redactNext = false
			continue
		}
		flagName, flagValue, hasValue := splitFlag(argument)
		if _, sensitive := sensitiveFlags[flagName]; sensitive {
			if hasValue {
				output[index] = argument[:len(argument)-len(flagValue)] + Replacement
			} else {
				output[index] = argument
				redactNext = true
			}
			continue
		}
		safe, truncated, err := redactor.valueWithURLContext(
			argument,
			incompleteURLPossible,
		)
		if err != nil {
			return nil, nil, err
		}
		output[index] = safe
		if truncated {
			truncation = appendUnique(truncation, "argv-value-truncated")
		}
	}
	if encodedStringBytes(output) > redactor.maxOutputBytes {
		return nil, nil, ErrRedactionFailed
	}
	return output, truncation, nil
}

func (redactor *Redactor) Text(value string) (string, []string, error) {
	safe, truncated, err := redactor.value(value)
	if err != nil {
		return "", nil, err
	}
	if len(safe) > redactor.maxOutputBytes {
		return "", nil, ErrRedactionFailed
	}
	if truncated {
		return safe, []string{"value-truncated"}, nil
	}
	return safe, nil, nil
}

func (redactor *Redactor) Activity(record workloadtypes.ActivityRecord) (workloadtypes.ActivityRecord, error) {
	if redactor == nil {
		return workloadtypes.ActivityRecord{}, ErrRedactionFailed
	}
	if err := record.Validate(); err != nil {
		return workloadtypes.ActivityRecord{}, fmt.Errorf("%w: %v", ErrRedactionFailed, err)
	}
	safe := record
	safe.RedactionStatus = workloadtypes.RedactionPassed
	safe.Truncation = append([]string(nil), record.Truncation...)

	var err error
	switch subject := record.Subject.(type) {
	case workloadtypes.ProcessSubject:
		safe.Subject, safe.Truncation, err = redactor.processSubject(subject, safe.Truncation)
	case *workloadtypes.ProcessSubject:
		if subject == nil {
			err = ErrRedactionFailed
			break
		}
		var redacted workloadtypes.ProcessSubject
		redacted, safe.Truncation, err = redactor.processSubject(*subject, safe.Truncation)
		safe.Subject = &redacted
	case workloadtypes.FileSubject:
		safe.Subject, safe.Truncation, err = redactor.fileSubject(subject, safe.Truncation)
	case *workloadtypes.FileSubject:
		if subject == nil {
			err = ErrRedactionFailed
			break
		}
		var redacted workloadtypes.FileSubject
		redacted, safe.Truncation, err = redactor.fileSubject(*subject, safe.Truncation)
		safe.Subject = &redacted
	case workloadtypes.NetworkSubject:
		safe.Subject, safe.Truncation, err = redactor.networkSubject(subject, safe.Truncation)
	case *workloadtypes.NetworkSubject:
		if subject == nil {
			err = ErrRedactionFailed
			break
		}
		var redacted workloadtypes.NetworkSubject
		redacted, safe.Truncation, err = redactor.networkSubject(*subject, safe.Truncation)
		safe.Subject = &redacted
	case workloadtypes.DNSSubject:
		safe.Subject, safe.Truncation, err = redactor.dnsSubject(subject, safe.Truncation)
	case *workloadtypes.DNSSubject:
		if subject == nil {
			err = ErrRedactionFailed
			break
		}
		var redacted workloadtypes.DNSSubject
		redacted, safe.Truncation, err = redactor.dnsSubject(*subject, safe.Truncation)
		safe.Subject = &redacted
	case workloadtypes.GenericSubject:
		safe.Subject, safe.Truncation, err = redactor.genericSubject(subject, safe.Truncation)
	case *workloadtypes.GenericSubject:
		if subject == nil {
			err = ErrRedactionFailed
			break
		}
		var redacted workloadtypes.GenericSubject
		redacted, safe.Truncation, err = redactor.genericSubject(*subject, safe.Truncation)
		safe.Subject = &redacted
	default:
		err = ErrRedactionFailed
	}
	if err != nil {
		return workloadtypes.ActivityRecord{}, err
	}
	if safe.Mediator != nil {
		mediator := *safe.Mediator
		mediator.ID, _, err = redactor.value(mediator.ID)
		if err != nil {
			return workloadtypes.ActivityRecord{}, err
		}
		safe.Mediator = &mediator
	}
	if safe.Actor != nil {
		actor := *safe.Actor
		actor.User, _, err = redactor.value(actor.User)
		if err != nil {
			return workloadtypes.ActivityRecord{}, err
		}
		actor.Group, _, err = redactor.value(actor.Group)
		if err != nil {
			return workloadtypes.ActivityRecord{}, err
		}
		safe.Actor = &actor
	}
	if err := safe.ValidatePersistable(); err != nil {
		return workloadtypes.ActivityRecord{}, fmt.Errorf("%w: %v", ErrRedactionFailed, err)
	}
	encoded, err := json.Marshal(safe)
	if err != nil || len(encoded) > redactor.maxOutputBytes || redactor.containsProtected(encoded) {
		return workloadtypes.ActivityRecord{}, ErrRedactionFailed
	}
	return safe, nil
}

// Execution removes protected material before an execution snapshot is
// eligible for persistence. Execution identity deliberately excludes argv,
// executable, cwd, and display identity, so redaction preserves its stable ID.
func (redactor *Redactor) Execution(
	execution workloadtypes.Execution,
) (workloadtypes.Execution, error) {
	if redactor == nil || execution.Validate() != nil {
		return workloadtypes.Execution{}, ErrRedactionFailed
	}
	safe := execution
	safe.Argv = append([]string(nil), execution.Argv...)
	safe.Limitations = append([]string(nil), execution.Limitations...)
	if execution.Exit != nil {
		exit := *execution.Exit
		if execution.Exit.Code != nil {
			code := *execution.Exit.Code
			exit.Code = &code
		}
		safe.Exit = &exit
	}

	var (
		err       error
		truncated bool
	)
	safe.Executable, truncated, err = redactor.value(execution.Executable)
	if err != nil {
		return workloadtypes.Execution{}, err
	}
	if truncated {
		safe.Limitations = appendUnique(
			safe.Limitations,
			"executable-truncated",
		)
	}
	argvTruncation := []string(nil)
	safe.Argv, argvTruncation, err = redactor.argv(
		execution.Argv,
		containsString(execution.Limitations, "argv-truncated"),
	)
	if err != nil {
		return workloadtypes.Execution{}, err
	}
	for _, reason := range argvTruncation {
		safe.Limitations = appendUnique(safe.Limitations, reason)
	}
	safe.Cwd, truncated, err = redactor.value(execution.Cwd)
	if err != nil {
		return workloadtypes.Execution{}, err
	}
	if truncated {
		safe.Limitations = appendUnique(safe.Limitations, "cwd-truncated")
	}
	safe.Identity.User, truncated, err = redactor.value(execution.Identity.User)
	if err != nil {
		return workloadtypes.Execution{}, err
	}
	if truncated {
		safe.Limitations = appendUnique(safe.Limitations, "user-truncated")
	}
	safe.Identity.Group, truncated, err = redactor.value(execution.Identity.Group)
	if err != nil {
		return workloadtypes.Execution{}, err
	}
	if truncated {
		safe.Limitations = appendUnique(safe.Limitations, "group-truncated")
	}
	sort.Strings(safe.Limitations)
	if len(safe.Limitations) > 16 || safe.Validate() != nil {
		return workloadtypes.Execution{}, ErrRedactionFailed
	}
	encoded, err := json.Marshal(safe)
	if err != nil || len(encoded) > redactor.maxOutputBytes ||
		redactor.containsProtected(encoded) {
		return workloadtypes.Execution{}, ErrRedactionFailed
	}
	return safe, nil
}

func (redactor *Redactor) processSubject(subject workloadtypes.ProcessSubject, truncation []string) (workloadtypes.ProcessSubject, []string, error) {
	var err error
	var truncated bool
	subject.Executable, truncated, err = redactor.value(subject.Executable)
	if err != nil {
		return workloadtypes.ProcessSubject{}, nil, err
	}
	if truncated {
		truncation = appendUnique(truncation, "executable-truncated")
	}
	var truncationForArgs []string
	subject.Argv, truncationForArgs, err = redactor.argv(
		subject.Argv,
		containsString(truncation, "argv-truncated"),
	)
	if err != nil {
		return workloadtypes.ProcessSubject{}, nil, err
	}
	for _, reason := range truncationForArgs {
		truncation = appendUnique(truncation, reason)
	}
	subject.Cwd, truncated, err = redactor.value(subject.Cwd)
	if err != nil {
		return workloadtypes.ProcessSubject{}, nil, err
	}
	if truncated {
		truncation = appendUnique(truncation, "cwd-truncated")
	}
	return subject, truncation, nil
}

func (redactor *Redactor) fileSubject(subject workloadtypes.FileSubject, truncation []string) (workloadtypes.FileSubject, []string, error) {
	safe, truncated, err := redactor.value(subject.Path)
	if err != nil {
		return workloadtypes.FileSubject{}, nil, err
	}
	subject.Path = safe
	if truncated {
		subject.PathState = "truncated"
		truncation = appendUnique(truncation, "path-truncated")
	}
	safe, truncated, err = redactor.value(subject.TargetPath)
	if err != nil {
		return workloadtypes.FileSubject{}, nil, err
	}
	subject.TargetPath = safe
	if truncated {
		truncation = appendUnique(truncation, "target-path-truncated")
	}
	return subject, truncation, nil
}

func (redactor *Redactor) networkSubject(subject workloadtypes.NetworkSubject, truncation []string) (workloadtypes.NetworkSubject, []string, error) {
	safe, truncated, err := redactor.value(subject.Domain)
	if err != nil {
		return workloadtypes.NetworkSubject{}, nil, err
	}
	subject.Domain = safe
	if truncated {
		truncation = appendUnique(truncation, "domain-truncated")
	}
	return subject, truncation, nil
}

func (redactor *Redactor) dnsSubject(subject workloadtypes.DNSSubject, truncation []string) (workloadtypes.DNSSubject, []string, error) {
	var err error
	var truncated bool
	subject.Query, truncated, err = redactor.value(subject.Query)
	if err != nil {
		return workloadtypes.DNSSubject{}, nil, err
	}
	if truncated {
		truncation = appendUnique(truncation, "domain-truncated")
	}
	subject.Answers = append([]string(nil), subject.Answers...)
	for index := range subject.Answers {
		subject.Answers[index], truncated, err = redactor.value(subject.Answers[index])
		if err != nil {
			return workloadtypes.DNSSubject{}, nil, err
		}
		if truncated {
			truncation = appendUnique(truncation, "dns-answer-truncated")
		}
	}
	subject.Resolver, truncated, err = redactor.value(subject.Resolver)
	if err != nil {
		return workloadtypes.DNSSubject{}, nil, err
	}
	if truncated {
		truncation = appendUnique(truncation, "resolver-truncated")
	}
	return subject, truncation, nil
}

func (redactor *Redactor) genericSubject(subject workloadtypes.GenericSubject, truncation []string) (workloadtypes.GenericSubject, []string, error) {
	safe, truncated, err := redactor.value(subject.Summary)
	if err != nil {
		return workloadtypes.GenericSubject{}, nil, err
	}
	subject.Summary = safe
	if truncated {
		truncation = appendUnique(truncation, "summary-truncated")
	}
	return subject, truncation, nil
}

func (redactor *Redactor) value(value string) (string, bool, error) {
	return redactor.valueWithURLContext(value, false)
}

func (redactor *Redactor) valueWithURLContext(
	value string,
	incompleteURLPossible bool,
) (string, bool, error) {
	if redactor == nil || !utf8.ValidString(value) {
		return "", false, ErrRedactionFailed
	}
	truncated := false
	if len(value) > redactor.maxValueBytes {
		value = validUTF8Prefix(value, redactor.maxValueBytes)
		value = redactor.redactBoundarySecretPrefix(value)
		truncated = true
	}
	value = replaceControls(value)
	for _, secret := range redactor.knownSecrets {
		secretText := string(secret)
		value = strings.ReplaceAll(value, secretText, Replacement)
		value = strings.ReplaceAll(value, url.QueryEscape(secretText), Replacement)
		value = strings.ReplaceAll(value, url.PathEscape(secretText), Replacement)
	}
	for _, token := range redactor.controlTokens {
		value = strings.ReplaceAll(value, token, Replacement)
		value = strings.ReplaceAll(value, url.QueryEscape(token), Replacement)
		value = strings.ReplaceAll(value, url.PathEscape(token), Replacement)
	}
	value = controlTokenPattern.ReplaceAllString(value, Replacement)
	value = authPattern.ReplaceAllString(value, `${1}`+Replacement)
	value = sensitiveAssignmentPattern.ReplaceAllString(value, `${1}${2}=`+Replacement)
	value = redactor.sanitizeURLs(value, incompleteURLPossible)
	if truncated {
		value += "…[truncated]"
	}
	if len(value) > redactor.maxOutputBytes || redactor.containsProtected([]byte(value)) || hasUnredactedCredentialSyntax(value) {
		return "", false, ErrRedactionFailed
	}
	return value, truncated, nil
}

func (redactor *Redactor) redactBoundarySecretPrefix(value string) string {
	for _, secret := range redactor.knownSecrets {
		secretText := string(secret)
		maximum := min(len(value), len(secretText)-1)
		for length := maximum; length > 0; length-- {
			if strings.HasSuffix(value, secretText[:length]) {
				value = value[:len(value)-length] + Replacement
				break
			}
		}
	}
	for _, token := range redactor.controlTokens {
		maximum := min(len(value), len(token)-1)
		for length := maximum; length > 0; length-- {
			if strings.HasSuffix(value, token[:length]) {
				value = value[:len(value)-length] + Replacement
				break
			}
		}
	}
	return value
}

func (redactor *Redactor) containsProtected(value []byte) bool {
	for _, secret := range redactor.knownSecrets {
		if bytes.Contains(value, secret) {
			return true
		}
	}
	for _, token := range redactor.controlTokens {
		if bytes.Contains(value, []byte(token)) {
			return true
		}
	}
	return false
}

func (redactor *Redactor) sanitizeURLs(
	value string,
	incompleteURLPossible bool,
) string {
	return urlPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if incompleteURLPossible {
			// Kernel argv capture is bounded. Without the missing suffix,
			// scheme://name and scheme://name:value are indistinguishable from
			// truncated URI userinfo. Redact the complete candidate instead of
			// preserving a possible username or password prefix.
			return redactedURLCandidate(candidate)
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return redactedURLCandidate(candidate)
		}
		parsed.User = nil
		query := parsed.Query()
		changed := false
		for key, values := range query {
			protected := false
			for _, queryValue := range values {
				if redactor.containsProtected([]byte(queryValue)) ||
					controlTokenPattern.MatchString(queryValue) {
					protected = true
					break
				}
			}
			if sensitiveQueryKey(key) || protected {
				query.Set(key, Replacement)
				changed = true
			}
		}
		if changed {
			parsed.RawQuery = query.Encode()
		}
		if redactor.containsProtected([]byte(parsed.Path)) ||
			controlTokenPattern.MatchString(parsed.Path) {
			parsed.Path = "/" + Replacement
			parsed.RawPath = ""
		}
		if parsed.Fragment != "" {
			// URL fragments frequently carry bearer material and are not needed
			// for activity attribution.
			parsed.Fragment = Replacement
		}
		safe := parsed.String()
		safe = strings.ReplaceAll(safe, "%5BREDACTED%5D", Replacement)
		return safe
	})
}

func redactedURLCandidate(candidate string) string {
	separator := strings.Index(candidate, "://")
	if separator <= 0 {
		return Replacement
	}
	return candidate[:separator] + "://redacted.invalid/" + Replacement
}

func sensitiveQueryKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
	switch normalized {
	case "token", "access_token", "api_key", "key", "password", "passwd",
		"secret", "authorization", "auth", "credential", "signature", "sig",
		"client_secret":
		return true
	default:
		return false
	}
}

func hasUnredactedCredentialSyntax(value string) bool {
	for _, candidate := range urlPattern.FindAllString(value, -1) {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return true
		}
		if parsed.User != nil {
			return true
		}
		for key, values := range parsed.Query() {
			if !sensitiveQueryKey(key) {
				continue
			}
			for _, queryValue := range values {
				if queryValue != Replacement {
					return true
				}
			}
		}
	}
	for _, match := range sensitiveAssignmentPattern.FindAllStringSubmatch(value, -1) {
		if len(match) >= 4 && match[3] != Replacement {
			return true
		}
	}
	for _, match := range authPattern.FindAllStringSubmatch(value, -1) {
		if len(match) >= 3 && match[2] != Replacement {
			return true
		}
	}
	return false
}

func splitFlag(argument string) (name, value string, hasValue bool) {
	trimmed := strings.TrimLeft(argument, "-")
	if trimmed == argument || trimmed == "" {
		if index := strings.IndexByte(argument, '='); index > 0 {
			return normalizeFlagName(argument[:index]), argument[index+1:], true
		}
		return normalizeFlagName(argument), "", false
	}
	if index := strings.IndexByte(trimmed, '='); index >= 0 {
		return normalizeFlagName(trimmed[:index]), trimmed[index+1:], true
	}
	return normalizeFlagName(trimmed), "", false
}

func normalizeFlagName(value string) string {
	return strings.ToLower(strings.ReplaceAll(value, "_", "-"))
}

func replaceControls(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for _, character := range value {
		if unicode.IsControl(character) {
			output.WriteRune('�')
			continue
		}
		output.WriteRune(character)
	}
	return output.String()
}

func validUTF8Prefix(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func encodedStringBytes(values []string) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}

func protectedEncodingVariants(value []byte) [][]byte {
	queryEscaped := url.QueryEscape(string(value))
	pathEscaped := url.PathEscape(string(value))
	values := [][]byte{
		append([]byte(nil), value...),
		[]byte(queryEscaped),
		[]byte(lowerPercentEscapes(queryEscaped)),
		[]byte(pathEscaped),
		[]byte(lowerPercentEscapes(pathEscaped)),
		[]byte(base64.StdEncoding.EncodeToString(value)),
		[]byte(base64.RawStdEncoding.EncodeToString(value)),
		[]byte(base64.URLEncoding.EncodeToString(value)),
		[]byte(base64.RawURLEncoding.EncodeToString(value)),
		[]byte(hex.EncodeToString(value)),
		[]byte(strings.ToUpper(hex.EncodeToString(value))),
	}
	result := make([][]byte, 0, len(values))
	for _, variant := range values {
		result = appendUniqueBytes(result, variant)
	}
	return result
}

func lowerPercentEscapes(value string) string {
	bytesValue := []byte(value)
	for index := 0; index+2 < len(bytesValue); index++ {
		if bytesValue[index] != '%' {
			continue
		}
		for offset := 1; offset <= 2; offset++ {
			character := bytesValue[index+offset]
			if character >= 'A' && character <= 'F' {
				bytesValue[index+offset] = character + ('a' - 'A')
			}
		}
		index += 2
	}
	return string(bytesValue)
}

func appendUniqueBytes(values [][]byte, value []byte) [][]byte {
	if len(value) == 0 {
		return values
	}
	for _, existing := range values {
		if bytes.Equal(existing, value) {
			return values
		}
	}
	return append(values, append([]byte(nil), value...))
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
