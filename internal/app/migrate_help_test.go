package app

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMigrateHelpIsCopyableAndCatalogListsEverySafetyFlag(t *testing.T) {
	var output bytes.Buffer
	a := app{stdout: &output, stderr: io.Discard, stdin: strings.NewReader("")}
	if err := a.migrateCommand([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
	help := output.String()
	for _, required := range []string{
		"--mode config",
		"--ack-guest-content --preview",
		"--include-secret <ref> --ack-secret-transfer",
		"--passphrase-stdin",
		"hideout migrate inspect ./dev.hideout-migration",
		"hideout migrate import ./dev.hideout-migration --preview",
		"config mode copies no VM disk",
		"guest disk is opaque",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("migration help is missing %q:\n%s", required, help)
		}
	}
	if strings.Contains(help, "HIDEOUT_SECRET_") {
		t.Fatalf("migration help suggested an environment-carried secret: %s", help)
	}

	entry, ok := defaultCommandCatalog().lookup("migrate")
	if !ok {
		t.Fatal("migration command is absent from the operator catalog")
	}
	flags := make(map[string]bool, len(entry.spec.Flags))
	for _, value := range entry.spec.Flags {
		flags[value.Name] = true
	}
	for _, required := range []string{
		"--ack-guest-content", "--include-secret", "--ack-secret-transfer",
		"--replace", "--secret", "--approve", "--idempotency-key",
		"--retain-partial", "--remove-partial",
	} {
		if !flags[required] {
			t.Fatalf("migration catalog is missing %s", required)
		}
	}
	for _, example := range entry.spec.Examples {
		if strings.Contains(example, "migrate export") &&
			!strings.Contains(example, "--mode config") &&
			!strings.Contains(example, "--ack-guest-content") {
			t.Fatalf("full export example omits required acknowledgement: %q", example)
		}
	}

	const (
		credentialCanary = "socks5://migration-user:migration-password@private.invalid"
		guestDataCanary  = "RAW_GUEST_DATA_SENTINEL_046"
	)
	envelope, err := json.Marshal(migrationAPIEnvelope{
		Version: manager.APIVersion,
		Errors:  []string{credentialCanary + " " + guestDataCanary},
		ErrorDetails: []manager.APIErrorDetail{{
			Code:     "migration.provider.failed",
			Message:  "provider exposed " + credentialCanary + " " + guestDataCanary,
			Recovery: "retry with " + credentialCanary + " " + guestDataCanary,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.migrationRequest = func(
		string, string, string, io.Reader,
	) ([]byte, error) {
		return append([]byte(nil), envelope...), nil
	}
	err = a.migrationAPI(
		profile.Store{Root: t.TempDir()}, "GET", "/api/v1/migration/status",
		nil, "migration/status", &struct{}{},
	)
	if err == nil {
		t.Fatal("unsafe Manager error response was accepted as success")
	}
	for _, forbidden := range []string{
		credentialCanary, "migration-password", guestDataCanary, "private.invalid",
	} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("migration CLI error leaked %q: %v", forbidden, err)
		}
	}
	if !strings.Contains(err.Error(), "migration.provider.failed") ||
		!strings.Contains(err.Error(), "refresh current state") {
		t.Fatalf("migration CLI error lost stable local guidance: %v", err)
	}
}
