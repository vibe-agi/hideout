package cmdgrammar

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostcap"
)

func parseGoto(value string) (string, *hostcap.Location, error) {
	// Parse from the right so POSIX filenames containing ':' remain valid.
	// The last numeric segment is the line unless the preceding segment is also
	// numeric, in which case they are line and column respectively.
	lastColon := strings.LastIndex(value, ":")
	if lastColon <= 0 || lastColon == len(value)-1 {
		return "", nil, &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "-g requires <file>:<line>[:<column>]"}
	}
	last, err := positiveInt(value[lastColon+1:])
	if err != nil {
		return "", nil, &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "-g line or column must be a positive integer"}
	}
	file := value[:lastColon]
	line, column := last, 1
	if previousColon := strings.LastIndex(file, ":"); previousColon >= 0 {
		if previous, parseErr := positiveInt(file[previousColon+1:]); parseErr == nil {
			file = file[:previousColon]
			line, column = previous, last
		}
	}
	if strings.TrimSpace(file) == "" {
		return "", nil, &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "-g value must start with a file"}
	}
	return file, &hostcap.Location{Line: line, Column: column}, nil
}

func positiveInt(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 0, &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "location must be a positive integer"}
	}
	return n, nil
}

// absGuestPath resolves a guest argument to an absolute, cleaned guest path. It
// does NOT touch the host filesystem or resolve to a host path; workspace
// containment and symlink escape are re-checked by Core against the session
// mapping.
func absGuestPath(arg, guestCWD string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "empty path argument"}
	}
	if strings.Contains(arg, "://") {
		return "", &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "URL targets are not supported"}
	}
	if strings.Contains(arg, "\x00") {
		return "", &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "path contains NUL"}
	}
	if filepath.IsAbs(arg) {
		return filepath.Clean(arg), nil
	}
	if strings.TrimSpace(guestCWD) == "" || !filepath.IsAbs(guestCWD) {
		return "", &hostcap.Error{Code: hostcap.CodePathNoHostMapping, Reason: "a relative path requires an absolute guest working directory"}
	}
	return filepath.Clean(filepath.Join(guestCWD, arg)), nil
}
