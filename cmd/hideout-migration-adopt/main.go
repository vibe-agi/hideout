package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/vibe-agi/hideout/internal/migration"
)

func main() {
	if len(os.Args) != 1 {
		fail("migration.adoption.arguments_forbidden")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		fail("migration.adoption.runtime_invalid")
	}
	selfPath, err := os.Executable()
	if err != nil {
		fail("migration.adoption.helper_unavailable")
	}
	selfPath, err = filepath.EvalSymlinks(selfPath)
	if err != nil {
		fail("migration.adoption.helper_unavailable")
	}
	if err := runGuestMigrationHelper(selfPath); err != nil {
		fail(err.Error())
	}
}

func runGuestMigrationHelper(selfPath string) error {
	schema, err := readGuestMigrationRequestSchema(migration.AdoptionGuestRequestPath)
	if err != nil {
		return &adoptionFailure{Code: "migration.adoption.request_invalid", Cause: err}
	}
	switch schema {
	case migration.AdoptionRequestSchema:
		return (adoptionRunner{
			rootPath:            string(filepath.Separator),
			requestPath:         migration.AdoptionGuestRequestPath,
			receiptPath:         migration.AdoptionGuestReceiptPath,
			selfPath:            selfPath,
			networkClassPath:    "/sys/class/net",
			random:              rand.Reader,
			generateSSHHostKeys: generateSSHHostKeys,
			fileOwnership: func(file *os.File, uid, gid int) error {
				return file.Chown(uid, gid)
			},
			shutdown: shutdownGuest,
		}).run()
	case migration.IdentityObservationRequestSchema:
		return (identityObservationRunner{
			rootPath:         string(filepath.Separator),
			requestPath:      migration.AdoptionGuestRequestPath,
			receiptPath:      migration.AdoptionGuestReceiptPath,
			selfPath:         selfPath,
			networkClassPath: "/sys/class/net",
			shutdown:         shutdownGuest,
		}).run()
	default:
		return &adoptionFailure{Code: "migration.adoption.request_invalid"}
	}
}

func fail(code string) {
	fmt.Fprintln(os.Stderr, code)
	os.Exit(1)
}
