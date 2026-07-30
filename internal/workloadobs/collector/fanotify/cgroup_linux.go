//go:build linux

package fanotify

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxCgroupMembershipBytes = 64 * 1024

type CgroupMatcher struct {
	procRoot          string
	targetUnifiedPath string
}

func NewCgroupMatcher(targetUnifiedPath string) (*CgroupMatcher, error) {
	return newCgroupMatcher("/proc", targetUnifiedPath)
}

func newCgroupMatcher(
	procRoot, targetUnifiedPath string,
) (*CgroupMatcher, error) {
	if !filepath.IsAbs(procRoot) ||
		filepath.Clean(procRoot) != procRoot ||
		len(procRoot) > maxObservedPathBytes ||
		!validUnifiedCgroupPath(targetUnifiedPath) ||
		targetUnifiedPath == "/" {
		return nil, ErrSourceConfig
	}
	info, err := os.Stat(procRoot)
	if err != nil || !info.IsDir() {
		return nil, errors.Join(ErrSourceConfig, err)
	}
	return &CgroupMatcher{
		procRoot: procRoot, targetUnifiedPath: targetUnifiedPath,
	}, nil
}

func (matcher *CgroupMatcher) Match(pid uint32) (bool, error) {
	if matcher == nil ||
		pid == 0 || pid > 4194304 {
		return false, ErrMembershipFilter
	}
	file, err := os.Open(filepath.Join(
		matcher.procRoot,
		strconv.FormatUint(uint64(pid), 10),
		"cgroup",
	))
	if err != nil {
		return false, errors.Join(ErrMembershipFilter, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCgroupMembershipBytes+1))
	if err != nil || len(data) > maxCgroupMembershipBytes {
		return false, errors.Join(ErrMembershipFilter, err)
	}
	unifiedPath, err := decodeUnifiedCgroupPath(string(data))
	if err != nil {
		return false, err
	}
	return unifiedPath == matcher.targetUnifiedPath ||
		strings.HasPrefix(
			unifiedPath,
			matcher.targetUnifiedPath+"/",
		), nil
}

func decodeUnifiedCgroupPath(value string) (string, error) {
	if value == "" || !utf8.ValidString(value) ||
		strings.IndexByte(value, 0) >= 0 {
		return "", ErrMembershipFilter
	}
	var unified string
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		if line == "" {
			if index == len(lines)-1 {
				continue
			}
			return "", ErrMembershipFilter
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			return "", ErrMembershipFilter
		}
		if _, err := strconv.ParseUint(parts[0], 10, 32); err != nil {
			return "", ErrMembershipFilter
		}
		if parts[0] == "0" && parts[1] == "" {
			if unified != "" ||
				!validUnifiedCgroupPath(parts[2]) {
				return "", ErrMembershipFilter
			}
			unified = parts[2]
		}
	}
	if unified == "" {
		return "", ErrMembershipFilter
	}
	return unified, nil
}

func validUnifiedCgroupPath(value string) bool {
	if value == "" ||
		len(value) > maxObservedPathBytes ||
		!strings.HasPrefix(value, "/") ||
		path.Clean(value) != value ||
		strings.IndexByte(value, 0) >= 0 ||
		!utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}
