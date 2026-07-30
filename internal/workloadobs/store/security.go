package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	privateDirectoryMode = 0o700
	privateFileMode      = 0o600
)

func prepareStoreRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root ||
		root == filepath.VolumeName(root)+string(filepath.Separator) {
		return ErrInvalidOptions
	}
	info, err := os.Lstat(root)
	switch {
	case err == nil:
		return validatePrivateDirectory(root, info)
	case !errors.Is(err, os.ErrNotExist):
		return errors.Join(ErrInsecurePath, err)
	}
	if err := os.MkdirAll(root, privateDirectoryMode); err != nil {
		return errors.Join(ErrInsecurePath, err)
	}
	if err := os.Chmod(root, privateDirectoryMode); err != nil {
		return errors.Join(ErrInsecurePath, err)
	}
	info, err = os.Lstat(root)
	if err != nil {
		return errors.Join(ErrInsecurePath, err)
	}
	return validatePrivateDirectory(root, info)
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		return validatePrivateDirectory(path, info)
	case !errors.Is(err, os.ErrNotExist):
		return errors.Join(ErrInsecurePath, err)
	}
	if err := os.Mkdir(path, privateDirectoryMode); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return errors.Join(ErrInsecurePath, err)
		}
	}
	info, err = os.Lstat(path)
	if err != nil {
		return errors.Join(ErrInsecurePath, err)
	}
	return validatePrivateDirectory(path, info)
}

func validatePrivateDirectory(path string, info os.FileInfo) error {
	if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != privateDirectoryMode {
		return fmt.Errorf("%w: directory %s must be a real 0700 directory",
			ErrInsecurePath, path)
	}
	return nil
}

func validatePrivateRegular(path string, info os.FileInfo) error {
	if info == nil || !info.Mode().IsRegular() ||
		info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != privateFileMode ||
		regularLinkCount(info) != 1 {
		return fmt.Errorf("%w: file %s must be a real 0600 file",
			ErrInsecurePath, path)
	}
	return nil
}

func regularLinkCount(info os.FileInfo) uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return uint64(stat.Nlink)
}

func openPrivateFile(path string, flags int, create bool) (*os.File, error) {
	unixFlags := flags | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if create {
		unixFlags |= unix.O_CREAT
	}
	fd, err := unix.Open(path, unixFlags, privateFileMode)
	if err != nil {
		return nil, errors.Join(ErrInsecurePath, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.Join(ErrInsecurePath, errors.New("open returned no file"))
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, errors.Join(ErrInsecurePath, err)
	}
	if err := validatePrivateRegular(path, info); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func createPrivateFile(path string) error {
	file, err := openPrivateFile(path, unix.O_WRONLY|unix.O_EXCL, true)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readPrivateFile(path string, maximum int64) ([]byte, error) {
	file, err := openPrivateFile(path, unix.O_RDONLY, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := io.LimitReader(file, maximum+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, ErrStoreCorrupt
	}
	return data, nil
}

func writeAtomicJSON(directory, name string, value any) ([]byte, error) {
	data, err := canonicalJSON(value)
	if err != nil {
		return nil, err
	}
	if err := writeAtomicBytes(directory, name, data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeAtomicBytes(directory, name string, data []byte) error {
	if strings.ContainsRune(name, filepath.Separator) || name == "" ||
		name == "." || name == ".." {
		return ErrInsecurePath
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return errors.Join(ErrInsecurePath, err)
	}
	if err := validatePrivateDirectory(directory, info); err != nil {
		return err
	}
	destination := filepath.Join(directory, name)
	if info, err := os.Lstat(destination); err == nil {
		if err := validatePrivateRegular(destination, info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrInsecurePath, err)
	}

	suffix, err := randomName(12)
	if err != nil {
		return err
	}
	temporary := filepath.Join(directory, "."+name+".tmp-"+suffix)
	file, err := openPrivateFile(
		temporary,
		unix.O_WRONLY|unix.O_EXCL,
		true,
	)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}
	if err := writeAll(file, data); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := syncDirectory(directory); err != nil {
		return err
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) != 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func randomName(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
