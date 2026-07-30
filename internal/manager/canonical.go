package manager

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

const (
	CanonicalDomainProfileProjection = "profile-projection"
	CanonicalDomainConfigurationPlan = "configuration-plan"
	CanonicalDomainOperationBinding  = "operation-binding"

	canonicalVersion  = "hideout.canonical-json/v1"
	maxCanonicalBytes = 4 << 20
	maxCanonicalDepth = 128
)

var canonicalDomainPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,127}$`)

// CanonicalDigest returns a versioned, domain-separated SHA-256 digest over
// deterministic JSON. Domain separation prevents identical JSON values used
// for different authorities from sharing a valid review digest.
func CanonicalDigest(domain string, value any) (string, error) {
	if !canonicalDomainPattern.MatchString(domain) {
		return "", fmt.Errorf("canonical digest domain %q is invalid", domain)
	}
	data, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(canonicalVersion))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// CanonicalJSON normalizes a typed Go value through JSON's data model and
// emits deterministic bytes. Map keys are ordered by encoding/json; HTML
// escaping is disabled because it changes bytes without changing the value.
// Unsupported values, trailing JSON, excessive size, and excessive nesting
// fail closed.
func CanonicalJSON(value any) ([]byte, error) {
	var first bytes.Buffer
	firstEncoder := json.NewEncoder(&first)
	firstEncoder.SetEscapeHTML(false)
	if err := firstEncoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode canonical JSON input: %w", err)
	}
	if first.Len() > maxCanonicalBytes {
		return nil, errors.New("canonical JSON input exceeds size bound")
	}

	decoder := json.NewDecoder(bytes.NewReader(first.Bytes()))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, fmt.Errorf("normalize canonical JSON input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("canonical JSON input contains trailing data")
		}
		return nil, fmt.Errorf("normalize canonical JSON trailing data: %w", err)
	}
	if err := validateCanonicalValue(normalized, 0); err != nil {
		return nil, err
	}

	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	result := bytes.TrimSuffix(output.Bytes(), []byte{'\n'})
	if len(result) > maxCanonicalBytes {
		return nil, errors.New("canonical JSON output exceeds size bound")
	}
	return append([]byte(nil), result...), nil
}

func validateCanonicalValue(value any, depth int) error {
	if depth > maxCanonicalDepth {
		return errors.New("canonical JSON exceeds nesting bound")
	}
	switch typed := value.(type) {
	case nil, bool, string, json.Number:
		return nil
	case []any:
		for _, item := range typed {
			if err := validateCanonicalValue(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, item := range typed {
			if err := validateCanonicalValue(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("canonical JSON contains unsupported value type %T", value)
	}
}
