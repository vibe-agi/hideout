package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"testing"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestCanariesAreAbsentFromEveryPostRedactionSink(t *testing.T) {
	const (
		managedSecret = "hideout/managed?secret=045"
		controlToken  = "token_hideout_control_045_fixture"
		uriUser       = "fixture-user"
		uriPassword   = "fixture-uri-password-045"
		authValue     = "fixture-authorization-045"
		flagValue     = "fixture-sensitive-flag-045"
		queryValue    = "fixture-sensitive-query-045"
		localPath     = "/Users/alice/projects/visible-local-path"
	)
	encodedSecrets := supportedCanaryEncodings(managedSecret)
	redactor, err := New(Config{
		KnownSecrets:  [][]byte{[]byte(managedSecret)},
		ControlTokens: []string{controlToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redactor.Clear)

	argv := []string{
		"agent",
		"--token", flagValue,
		"--password=" + flagValue,
		"Authorization: Bearer " + authValue,
		"socks5://" + uriUser + ":" + uriPassword + "@127.0.0.1:7890",
		"https://example.test/path?ok=1&access_token=" + queryValue,
		controlToken,
		localPath,
	}
	argv = append(argv, encodedSecrets...)
	record := processRecordFixture(argv)
	safe, err := redactor.Activity(record)
	if err != nil {
		t.Fatalf("redact canary activity: %v", err)
	}
	subject, ok := safe.Subject.(workloadtypes.ProcessSubject)
	if !ok {
		t.Fatalf("safe subject type=%T", safe.Subject)
	}

	persisted, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(map[string]any{
		"argv":  subject.Argv,
		"path":  subject.Cwd,
		"actor": safe.Actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := json.Marshal(struct {
		Schema  string                         `json:"schema"`
		Records []workloadtypes.ActivityRecord `json:"records"`
	}{
		Schema: "hideout.activity-export.v1", Records: []workloadtypes.ActivityRecord{safe},
	})
	if err != nil {
		t.Fatal(err)
	}
	var logBuffer bytes.Buffer
	logger := log.New(&logBuffer, "", 0)
	logger.Printf("activity=%+v", safe)

	sinks := map[string][]byte{
		"persisted": persisted,
		"index":     index,
		"render":    []byte(fmt.Sprintf("%+v", safe)),
		"export":    exported,
		"log":       logBuffer.Bytes(),
	}
	forbidden := []string{
		managedSecret,
		controlToken,
		uriUser,
		uriPassword,
		authValue,
		flagValue,
		queryValue,
	}
	forbidden = append(forbidden, encodedSecrets...)
	for sink, payload := range sinks {
		t.Run(sink, func(t *testing.T) {
			for _, canary := range uniqueCanaries(forbidden) {
				if bytes.Contains(payload, []byte(canary)) {
					t.Errorf("%s retained canary %q: %s", sink, canary, payload)
				}
			}
		})
	}
	if !bytes.Contains(persisted, []byte(localPath)) {
		t.Fatalf("local path was hidden before the share boundary: %s", persisted)
	}
}

func TestCanaryMatrixCoversManagedEncodingsAndCredentialSyntax(t *testing.T) {
	const secret = "encoding/matrix?secret=045"
	redactor, err := New(Config{KnownSecrets: [][]byte{[]byte(secret)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redactor.Clear)

	for _, encoded := range supportedCanaryEncodings(secret) {
		t.Run(encodedCanaryName(encoded), func(t *testing.T) {
			safe, _, err := redactor.Text("prefix=" + encoded)
			if err != nil {
				t.Fatalf("redact encoding: %v", err)
			}
			if strings.Contains(safe, encoded) ||
				!strings.Contains(safe, Replacement) {
				t.Fatalf("encoding survived: input=%q output=%q", encoded, safe)
			}
		})
	}

	argv, _, err := redactor.Argv([]string{
		"client",
		"--api-key", "split-field-canary",
		"--header", "Authorization: Bearer auth-field-canary",
		"https://user-canary:password-canary@example.test/?token=query-canary",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, "\n")
	for _, forbidden := range []string{
		"split-field-canary",
		"auth-field-canary",
		"user-canary",
		"password-canary",
		"query-canary",
	} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("credential syntax retained %q: %s", forbidden, joined)
		}
	}
}

func supportedCanaryEncodings(secret string) []string {
	raw := []byte(secret)
	values := []string{
		url.QueryEscape(secret),
		url.PathEscape(secret),
		base64.StdEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(raw),
		hex.EncodeToString(raw),
		strings.ToUpper(hex.EncodeToString(raw)),
	}
	return uniqueCanaries(values)
}

func uniqueCanaries(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func encodedCanaryName(value string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"?", "_",
		"+", "_",
		"=", "_",
		"%", "_",
	)
	name := replacer.Replace(value)
	if len(name) > 48 {
		name = name[:48]
	}
	return name
}
