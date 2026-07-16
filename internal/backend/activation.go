package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ActivationReceiptSchema = "hideout.environment-activation/v1"
	activationReceiptFile   = "activation.json"
	maxActivationConfigSize = 4 << 20
)

var (
	activationEnvironmentPattern = regexp.MustCompile(`^env_[a-z0-9]+$`)
	activationSessionPattern     = regexp.MustCompile(`^ses_[A-Za-z0-9_]+$`)
	activationInstancePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	activationDigestPattern      = regexp.MustCompile(`^[a-f0-9]{64}$`)
	activationBootIDPattern      = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

// ActivationReceipt binds the warm-attach path to one fully checked boot and
// the exact live owner that established it. It contains no raw host paths,
// process identifiers, credentials, or target-supplied values.
type ActivationReceipt struct {
	Schema                string    `json:"schema"`
	EnvironmentID         string    `json:"environmentId"`
	InstanceName          string    `json:"instanceName"`
	ConfigSHA256          string    `json:"configSha256"`
	RuntimeIdentitySHA256 string    `json:"runtimeIdentitySha256"`
	BootID                string    `json:"bootId"`
	NamespaceProbe        bool      `json:"namespaceProbe"`
	OwnerSessionID        string    `json:"ownerSessionId"`
	ObservedAt            time.Time `json:"observedAt"`
}

// WarmActivationReceiptProvider supplies the exact owner that established a
// locally validated activation receipt. Manager independently proves that
// owner live before invoking WarmActivate.
type WarmActivationReceiptProvider interface {
	WarmActivationOwner(session *Session) (string, error)
}

func BuildActivationReceipt(session *Session, bootID string, observedAt time.Time) (ActivationReceipt, error) {
	configDigest, identityDigest, err := ActivationIdentity(session)
	if err != nil {
		return ActivationReceipt{}, err
	}
	receipt := ActivationReceipt{
		Schema: ActivationReceiptSchema, EnvironmentID: session.EnvironmentID, InstanceName: session.InstanceName,
		ConfigSHA256: configDigest, RuntimeIdentitySHA256: identityDigest, BootID: strings.TrimSpace(bootID),
		NamespaceProbe: true, OwnerSessionID: session.ID, ObservedAt: observedAt.UTC(),
	}
	if err := receipt.Validate(); err != nil {
		return ActivationReceipt{}, err
	}
	return receipt, nil
}

func (r ActivationReceipt) Validate() error {
	if r.Schema != ActivationReceiptSchema {
		return fmt.Errorf("unsupported activation receipt schema %q", r.Schema)
	}
	if !activationEnvironmentPattern.MatchString(r.EnvironmentID) {
		return fmt.Errorf("invalid activation environment %q", r.EnvironmentID)
	}
	if !activationInstancePattern.MatchString(r.InstanceName) {
		return errors.New("activation instance name is invalid")
	}
	if !activationDigestPattern.MatchString(r.ConfigSHA256) || !activationDigestPattern.MatchString(r.RuntimeIdentitySHA256) {
		return errors.New("activation identity digest is invalid")
	}
	if !activationBootIDPattern.MatchString(r.BootID) {
		return errors.New("activation boot identity is invalid")
	}
	if !r.NamespaceProbe {
		return errors.New("activation namespace probe did not pass")
	}
	if !activationSessionPattern.MatchString(r.OwnerSessionID) {
		return errors.New("activation owner session is invalid")
	}
	if r.ObservedAt.IsZero() {
		return errors.New("activation observation time is required")
	}
	return nil
}

func (r ActivationReceipt) MatchesSession(session *Session) error {
	if err := r.Validate(); err != nil {
		return err
	}
	configDigest, identityDigest, err := ActivationIdentity(session)
	if err != nil {
		return err
	}
	if r.EnvironmentID != session.EnvironmentID || r.InstanceName != session.InstanceName ||
		r.ConfigSHA256 != configDigest || r.RuntimeIdentitySHA256 != identityDigest {
		return errors.New("activation receipt does not match the prepared environment")
	}
	return nil
}

func ActivationIdentity(session *Session) (string, string, error) {
	if session == nil || strings.TrimSpace(session.ConfigPath) == "" {
		return "", "", errors.New("activation identity requires a prepared config")
	}
	info, err := os.Lstat(session.ConfigPath)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxActivationConfigSize {
		return "", "", errors.New("activation config must be a bounded regular file")
	}
	config, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		return "", "", err
	}
	configSum := sha256.Sum256(config)
	identity := struct {
		EnvironmentID string                      `json:"environmentId"`
		InstanceName  string                      `json:"instanceName"`
		ConfigSHA256  string                      `json:"configSha256"`
		Contract      *RuntimeContract            `json:"runtimeContract,omitempty"`
		Expected      *RuntimeInstanceExpectation `json:"runtimeExpected,omitempty"`
	}{
		EnvironmentID: session.EnvironmentID, InstanceName: session.InstanceName,
		ConfigSHA256: hex.EncodeToString(configSum[:]), Contract: CloneRuntimeContract(session.RuntimeContract),
	}
	if session.RuntimeInstanceExpected != nil {
		copy := *session.RuntimeInstanceExpected
		identity.Expected = &copy
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return "", "", err
	}
	identitySum := sha256.Sum256(data)
	return identity.ConfigSHA256, hex.EncodeToString(identitySum[:]), nil
}

func WriteActivationReceipt(runtimeRoot string, receipt ActivationReceipt) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if err := validateActivationRoot(runtimeRoot); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := filepath.Join(runtimeRoot, activationReceiptFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(runtimeRoot, activationReceiptFile)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func LoadActivationReceipt(runtimeRoot string) (ActivationReceipt, error) {
	if err := validateActivationRoot(runtimeRoot); err != nil {
		return ActivationReceipt{}, err
	}
	path := filepath.Join(runtimeRoot, activationReceiptFile)
	info, err := os.Lstat(path)
	if err != nil {
		return ActivationReceipt{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16<<10 {
		return ActivationReceipt{}, errors.New("activation receipt must be a bounded regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ActivationReceipt{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt ActivationReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return ActivationReceipt{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ActivationReceipt{}, errors.New("activation receipt contains trailing data")
		}
		return ActivationReceipt{}, err
	}
	if err := receipt.Validate(); err != nil {
		return ActivationReceipt{}, err
	}
	return receipt, nil
}

func RemoveActivationReceipt(runtimeRoot string) error {
	if strings.TrimSpace(runtimeRoot) == "" {
		return nil
	}
	if err := validateActivationRoot(runtimeRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, name := range []string{activationReceiptFile + ".tmp", activationReceiptFile} {
		path := filepath.Join(runtimeRoot, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing to remove non-regular activation receipt")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func validateActivationRoot(root string) error {
	if !filepath.IsAbs(root) || strings.TrimSpace(root) == "" {
		return errors.New("activation runtime root must be absolute")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("activation runtime root must be a real directory")
	}
	return nil
}
