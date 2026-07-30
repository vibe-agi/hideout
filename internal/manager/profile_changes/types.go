package profilechanges

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	KindNetworkPosture        = "network.posture"
	KindNetworkProxyRef       = "network.proxyRef"
	KindNetworkDNS            = "network.dns"
	KindProfileEnvironment    = "profile.environment"
	KindProfileHostFS         = "profile.hostfs"
	KindProfileCommandProxy   = "profile.commandProxy"
	KindProfileCommandAdapter = "profile.commandAdapter"
	KindActivityRetention     = "activity.retention"

	maxChangeBytes = 64 << 10

	environmentValueProvided = "[value provided]"
)

var (
	ErrInvalidChange = errors.New("profile typed change is invalid")
	ErrUnknownChange = errors.New("profile typed change kind is unknown")
	ErrNoChange      = errors.New("profile typed change has no effect")
)

type Change struct {
	Kind  string
	Value json.RawMessage
}

type Options struct {
	ProfileDir string
	Now        func() time.Time
}

type Diff struct {
	Kind   string
	Field  string
	Before string
	After  string
	Scope  string
}

type Warning struct {
	Code    string
	Summary string
}

type Result struct {
	Desired  profile.Profile
	Diff     []Diff
	Warnings []Warning
}

func Normalize(kind string, raw json.RawMessage) (json.RawMessage, error) {
	var (
		value any
		err   error
	)
	switch kind {
	case KindNetworkPosture:
		value, err = normalizeNetworkPosture(raw)
	case KindNetworkProxyRef:
		value, err = normalizeNetworkProxyRef(raw)
	case KindNetworkDNS:
		value, err = normalizeNetworkDNS(raw)
	case KindProfileEnvironment:
		value, err = normalizeEnvironment(raw)
	case KindProfileHostFS:
		value, err = normalizeHostFS(raw)
	case KindProfileCommandProxy:
		value, err = normalizeCommandProxy(raw)
	case KindProfileCommandAdapter:
		value, err = normalizeCommandAdapter(raw)
	case KindActivityRetention:
		value, err = normalizeActivityRetention(raw)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownChange, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidChange, kind, err)
	}
	return canonicalJSON(value)
}

// Review converts a private canonical change into its public review form. It
// is idempotent so ConfigurationPlan validation can reject any non-review
// representation without access to the private operation record.
func Review(kind string, normalized json.RawMessage) (json.RawMessage, error) {
	if kind != KindProfileEnvironment {
		return Normalize(kind, normalized)
	}
	value, err := normalizeEnvironment(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidChange, kind, err)
	}
	for name := range value.Set {
		value.Set[name] = environmentValueProvided
	}
	return canonicalJSON(value)
}

func Build(
	current profile.Profile,
	changes []Change,
	options Options,
) (Result, error) {
	if current.Validate() != nil || len(changes) == 0 || len(changes) > 64 {
		return Result{}, ErrInvalidChange
	}
	desired, err := cloneProfile(current)
	if err != nil {
		return Result{}, err
	}
	normalized := make([]Change, len(changes))
	seen := make(map[string]struct{}, len(changes))
	for index, change := range changes {
		if _, duplicate := seen[change.Kind]; duplicate {
			return Result{}, fmt.Errorf(
				"%w: duplicate %s",
				ErrInvalidChange,
				change.Kind,
			)
		}
		seen[change.Kind] = struct{}{}
		value, err := Normalize(change.Kind, change.Value)
		if err != nil {
			return Result{}, err
		}
		normalized[index] = Change{Kind: change.Kind, Value: value}
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].Kind < normalized[right].Kind
	})

	result := Result{Desired: desired}
	for _, change := range normalized {
		var (
			diff     []Diff
			warnings []Warning
		)
		switch change.Kind {
		case KindNetworkPosture:
			diff, err = applyNetworkPosture(&result.Desired, change.Value)
		case KindNetworkProxyRef:
			diff, err = applyNetworkProxyRef(&result.Desired, change.Value)
		case KindNetworkDNS:
			diff, err = applyNetworkDNS(&result.Desired, change.Value)
		case KindProfileEnvironment:
			diff, err = applyEnvironment(&result.Desired, change.Value)
		case KindProfileHostFS:
			diff, warnings, err = applyHostFS(
				&result.Desired,
				change.Value,
			)
		case KindProfileCommandProxy:
			diff, err = applyCommandProxy(&result.Desired, change.Value)
		case KindProfileCommandAdapter:
			diff, warnings, err = applyCommandAdapter(
				&result.Desired,
				change.Value,
				options,
			)
		case KindActivityRetention:
			diff, err = applyActivityRetention(
				&result.Desired,
				change.Value,
			)
		default:
			err = ErrUnknownChange
		}
		if err != nil {
			return Result{}, fmt.Errorf(
				"%w: %s: %v",
				ErrInvalidChange,
				change.Kind,
				err,
			)
		}
		result.Diff = append(result.Diff, diff...)
		result.Warnings = append(result.Warnings, warnings...)
	}
	if err := validateCompleteNetwork(result.Desired); err != nil {
		return Result{}, fmt.Errorf("%w: desired network: %v", ErrInvalidChange, err)
	}
	if err := result.Desired.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: desired profile: %v", ErrInvalidChange, err)
	}
	equal, err := profilesEqual(current, result.Desired)
	if err != nil {
		return Result{}, err
	}
	if equal {
		return Result{}, ErrNoChange
	}
	sort.Slice(result.Diff, func(left, right int) bool {
		leftKey := result.Diff[left].Kind + "\x00" +
			result.Diff[left].Field
		rightKey := result.Diff[right].Kind + "\x00" +
			result.Diff[right].Field
		return leftKey < rightKey
	})
	sort.Slice(result.Warnings, func(left, right int) bool {
		return result.Warnings[left].Code < result.Warnings[right].Code
	})
	return result, nil
}

func decodeStrict(raw json.RawMessage, target any) error {
	if len(raw) == 0 || len(raw) > maxChangeBytes {
		return errors.New("change value exceeds size bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("change value contains trailing JSON")
		}
		return err
	}
	return nil
}

func canonicalJSON(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > maxChangeBytes {
		return nil, errors.New("canonical change exceeds size bound")
	}
	return json.RawMessage(data), nil
}

func cloneProfile(value profile.Profile) (profile.Profile, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return profile.Profile{}, err
	}
	var cloned profile.Profile
	if err := json.Unmarshal(data, &cloned); err != nil {
		return profile.Profile{}, err
	}
	return cloned, nil
}

func profilesEqual(left, right profile.Profile) (bool, error) {
	leftData, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	rightData, err := json.Marshal(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftData, rightData), nil
}

func state(value bool, present, absent string) string {
	if value {
		return present
	}
	return absent
}
