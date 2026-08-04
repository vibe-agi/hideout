package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
)

const maximumAdoptionAuthorizedKeysBytes = 1 << 20

const migrationCloudInitSSHPolicy = `# Managed by Hideout migration.
# Lima changes the cloud-init instance-id on every boot; retain the identity
# proved by isolated adoption and keep the destination root control key usable.
ssh_deletekeys: false
disable_root: false
`

type adoptionGuestAccount struct {
	home string
	uid  int
	gid  int
}

func installDestinationSSHKeys(
	rootPath string,
	user string,
	keys []string,
	setOwnership func(*os.File, int, int) error,
) error {
	if setOwnership == nil {
		return errors.New("destination SSH ownership setter is unavailable")
	}
	canonical, err := canonicalDestinationSSHKeys(keys)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	account, err := readAdoptionGuestAccount(root, user)
	if err != nil {
		return err
	}
	for _, target := range []adoptionGuestAccount{
		account,
		{home: "root", uid: 0, gid: 0},
	} {
		if err := installAccountSSHKeys(root, target, canonical, setOwnership); err != nil {
			return err
		}
	}
	return installMigrationCloudInitSSHPolicy(root, setOwnership)
}

func canonicalDestinationSSHKeys(keys []string) ([]string, error) {
	if len(keys) == 0 || len(keys) > 32 {
		return nil, errors.New("destination SSH key set is empty or oversized")
	}
	result := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, value := range keys {
		publicKey, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(value + "\n"))
		if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("destination SSH key is invalid")
		}
		identity := string(publicKey.Marshal())
		if _, exists := seen[identity]; exists {
			return nil, errors.New("destination SSH key is duplicated")
		}
		seen[identity] = struct{}{}
		result = append(result, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))))
	}
	return result, nil
}

func readAdoptionGuestAccount(root *os.Root, user string) (adoptionGuestAccount, error) {
	if user == "" || user == "root" || strings.ContainsAny(user, ":/\x00\r\n") {
		return adoptionGuestAccount{}, errors.New("destination SSH user is invalid")
	}
	data, err := readAdoptionRootFile(root, "etc/passwd", maximumAdoptionAuthorizedKeysBytes)
	if err != nil {
		return adoptionGuestAccount{}, err
	}
	wantHome := "/home/" + user
	var result adoptionGuestAccount
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || fields[0] != user {
			continue
		}
		if found || fields[5] != wantHome {
			return adoptionGuestAccount{}, errors.New("destination SSH user account is ambiguous")
		}
		uid, uidErr := strconv.Atoi(fields[2])
		gid, gidErr := strconv.Atoi(fields[3])
		if uidErr != nil || gidErr != nil || uid <= 0 || gid < 0 {
			return adoptionGuestAccount{}, errors.New("destination SSH user ownership is invalid")
		}
		result = adoptionGuestAccount{
			home: strings.TrimPrefix(wantHome, "/"), uid: uid, gid: gid,
		}
		found = true
	}
	if !found {
		return adoptionGuestAccount{}, errors.New("destination SSH user is absent")
	}
	return result, nil
}

func installAccountSSHKeys(
	root *os.Root,
	account adoptionGuestAccount,
	keys []string,
	setOwnership func(*os.File, int, int) error,
) error {
	if err := requireAdoptionDirectory(root, account.home); err != nil {
		return err
	}
	sshDirectory := filepath.Join(account.home, ".ssh")
	if err := ensureAdoptionSSHDirectory(
		root, sshDirectory, account.uid, account.gid, setOwnership,
	); err != nil {
		return err
	}
	authorizedKeys := filepath.Join(sshDirectory, "authorized_keys")
	existing, err := readAdoptionRootFile(
		root, authorizedKeys, maximumAdoptionAuthorizedKeysBytes,
	)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	updated := append([]byte(nil), existing...)
	identities := unrestrictedAuthorizedKeyIdentities(existing)
	for _, key := range keys {
		publicKey, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(key + "\n"))
		identity := string(publicKey.Marshal())
		if _, exists := identities[identity]; exists {
			continue
		}
		if len(updated) != 0 && updated[len(updated)-1] != '\n' {
			updated = append(updated, '\n')
		}
		updated = append(updated, key...)
		updated = append(updated, '\n')
		identities[identity] = struct{}{}
	}
	if len(updated) > maximumAdoptionAuthorizedKeysBytes {
		return errors.New("destination authorized_keys exceeds its bound")
	}
	if bytes.Equal(existing, updated) && err == nil {
		return protectAdoptionRootFile(
			root, authorizedKeys, 0o600, account.uid, account.gid, setOwnership,
		)
	}
	return replaceAdoptionRootFile(
		root, sshDirectory, authorizedKeys,
		filepath.Join(sshDirectory, ".authorized_keys.hideout.tmp"),
		updated, 0o600, account.uid, account.gid, setOwnership,
	)
}

func installMigrationCloudInitSSHPolicy(
	root *os.Root,
	setOwnership func(*os.File, int, int) error,
) error {
	for _, directory := range []string{"etc/cloud", "etc/cloud/cloud.cfg.d"} {
		if err := requireAdoptionDirectory(root, directory); err != nil {
			return err
		}
	}
	directory := "etc/cloud/cloud.cfg.d"
	path := filepath.Join(directory, "99-hideout-migration-identity.cfg")
	expected := []byte(migrationCloudInitSSHPolicy)
	observed, err := readAdoptionRootFile(root, path, maximumAdoptionAuthorizedKeysBytes)
	if err == nil {
		if !bytes.Equal(observed, expected) {
			return errors.New("migration cloud-init identity policy changed")
		}
		return protectAdoptionRootFile(root, path, 0o644, 0, 0, setOwnership)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return replaceAdoptionRootFile(
		root, directory, path,
		filepath.Join(directory, ".99-hideout-migration-identity.cfg.tmp"),
		expected, 0o644, 0, 0, setOwnership,
	)
}

func requireAdoptionDirectory(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("adoption directory %q is unsafe", name)
	}
	return nil
}

func ensureAdoptionSSHDirectory(
	root *os.Root,
	name string,
	uid, gid int,
	setOwnership func(*os.File, int, int) error,
) error {
	info, err := root.Lstat(name)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(name, 0o700); err != nil {
			return err
		}
		created = true
		info, err = root.Lstat(name)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("destination .ssh directory is unsafe")
	}
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("destination .ssh directory changed during adoption")
	}
	if err := directory.Chmod(0o700); err != nil {
		return err
	}
	if err := setOwnership(directory, uid, gid); err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		return err
	}
	if !created {
		return nil
	}
	parent, err := root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func readAdoptionRootFile(root *os.Root, name string, limit int) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() ||
		before.Size() < 0 || before.Size() > int64(limit) ||
		!adoptionFileHasSingleLink(before) {
		return nil, errors.New("adoption file is aliased, special, or oversized")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("adoption file changed before inspection")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("adoption file exceeded its read bound")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() ||
		!after.ModTime().Equal(opened.ModTime()) || !adoptionFileHasSingleLink(after) {
		return nil, errors.New("adoption file changed during inspection")
	}
	return data, nil
}

func unrestrictedAuthorizedKeyIdentities(data []byte) map[string]struct{} {
	result := make(map[string]struct{})
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		encoded := append(append([]byte(nil), line...), '\n')
		publicKey, _, options, rest, err := ssh.ParseAuthorizedKey(encoded)
		if err == nil && len(options) == 0 && len(bytes.TrimSpace(rest)) == 0 {
			result[string(publicKey.Marshal())] = struct{}{}
		}
	}
	return result
}

func protectAdoptionRootFile(
	root *os.Root,
	name string,
	mode os.FileMode,
	uid, gid int,
	setOwnership func(*os.File, int, int) error,
) error {
	before, err := root.Lstat(name)
	if err != nil || before.Mode()&os.ModeSymlink != 0 ||
		!before.Mode().IsRegular() || !adoptionFileHasSingleLink(before) {
		return errors.New("adoption file is unsafe to protect")
	}
	file, err := root.OpenFile(name, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !adoptionFileHasSingleLink(opened) {
		return errors.New("adoption file changed before protection")
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := setOwnership(file, uid, gid); err != nil {
		return err
	}
	return file.Sync()
}

func replaceAdoptionRootFile(
	root *os.Root,
	directory, name, temporary string,
	data []byte,
	mode os.FileMode,
	uid, gid int,
	setOwnership func(*os.File, int, int) error,
) (retErr error) {
	file, err := root.OpenFile(
		temporary,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW,
		mode,
	)
	if err != nil {
		return err
	}
	fileOpen := true
	removeTemporary := true
	defer func() {
		if fileOpen {
			retErr = errors.Join(retErr, file.Close())
		}
		if removeTemporary {
			retErr = errors.Join(retErr, root.Remove(temporary))
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	created, err := file.Stat()
	if err != nil || !created.Mode().IsRegular() || !adoptionFileHasSingleLink(created) {
		return errors.New("adoption temporary file acquired an alias")
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := setOwnership(file, uid, gid); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	closeErr := file.Close()
	fileOpen = false
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	removeTemporary = false
	directoryHandle, err := root.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func adoptionFileHasSingleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
