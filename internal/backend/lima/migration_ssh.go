package lima

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const migrationLimaSSHKeyLimit = 64 << 10

type migrationLimaSSHKeygen func(context.Context, string) error

// destinationLimaSSHKey returns the control key that stock Lima will place in
// ssh.config after activation. Full import bypasses Lima's ordinary cloud-init
// generation, so a fresh destination must initialize the same _config/user key
// under the same directory lock used by Lima before the isolated adoption boot.
func destinationLimaSSHKey(ctx context.Context, home string) (string, error) {
	return destinationLimaSSHKeyWithGenerator(ctx, home, generateMigrationLimaSSHKey)
}

func destinationLimaSSHKeyWithGenerator(
	ctx context.Context,
	home string,
	generate migrationLimaSSHKeygen,
) (result string, retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if generate == nil {
		return "", errors.New("destination Lima SSH key generator is unavailable")
	}
	configDir, err := ensurePrivateMigrationDirectory(home, "_config")
	if err != nil {
		return "", err
	}
	directory, err := os.Open(configDir)
	if err != nil {
		return "", err
	}
	defer func() { retErr = errors.Join(retErr, directory.Close()) }()
	if err := unix.Flock(int(directory.Fd()), unix.LOCK_EX); err != nil {
		return "", err
	}
	defer func() {
		retErr = errors.Join(retErr, unix.Flock(int(directory.Fd()), unix.LOCK_UN))
	}()

	privatePath := filepath.Join(configDir, "user")
	publicPath := filepath.Join(configDir, "user.pub")
	privateExists, err := migrationRegularPathExists(privatePath)
	if err != nil {
		return "", err
	}
	publicExists, err := migrationRegularPathExists(publicPath)
	if err != nil {
		return "", err
	}
	if privateExists != publicExists {
		return "", errors.New("destination Lima SSH key pair is incomplete")
	}
	if !privateExists {
		if err := generate(ctx, privatePath); err != nil {
			return "", err
		}
		if err := syncMigrationDirectory(configDir); err != nil {
			return "", err
		}
	}
	return validateDestinationLimaSSHKeyPair(privatePath, publicPath)
}

func migrationRegularPathExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("destination Lima SSH key path is not a regular file")
	}
	return true, nil
}

func generateMigrationLimaSSHKey(ctx context.Context, privatePath string) error {
	command := exec.CommandContext(
		ctx,
		"/usr/bin/ssh-keygen",
		"-t", "ed25519", "-q", "-N", "", "-C", "lima", "-f", privatePath,
	)
	command.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
	var output boundedRuntimeCapture
	output.limit = 4096
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("generate destination Lima SSH control key")
	}
	if output.truncated {
		return errors.New("destination Lima SSH key generator output exceeded its bound")
	}
	return nil
}

func validateDestinationLimaSSHKeyPair(privatePath, publicPath string) (string, error) {
	privateData, privateInfo, err := readStableMigrationFile(
		privatePath, migrationLimaSSHKeyLimit,
	)
	if err != nil || privateInfo.Mode().Perm()&0o077 != 0 ||
		privateInfo.Mode().Perm()&0o111 != 0 || privateInfo.Mode().Perm()&0o400 == 0 {
		clear(privateData)
		return "", errors.New("destination Lima SSH private key is unsafe")
	}
	signer, err := ssh.ParsePrivateKey(privateData)
	clear(privateData)
	if err != nil {
		return "", errors.New("destination Lima SSH private key is invalid")
	}
	publicData, publicInfo, err := readStableMigrationFile(
		publicPath, migrationLimaSSHKeyLimit,
	)
	if err != nil || publicInfo.Mode().Perm()&0o022 != 0 ||
		publicInfo.Mode().Perm()&0o111 != 0 || publicInfo.Mode().Perm()&0o400 == 0 {
		return "", errors.New("destination Lima SSH public key is unsafe")
	}
	publicKey, _, options, rest, err := ssh.ParseAuthorizedKey(publicData)
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 ||
		!bytes.Equal(publicKey.Marshal(), signer.PublicKey().Marshal()) {
		return "", errors.New("destination Lima SSH public key does not match its private key")
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))), nil
}
