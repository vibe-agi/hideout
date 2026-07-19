package workspaceattach

import (
	"errors"
	"os"
	"path/filepath"
)

func openPortalRootAuthority(path string) (string, *os.Root, os.FileInfo, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, nil, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", nil, nil, err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return "", nil, nil, err
	}
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return "", nil, nil, err
	}
	if !info.IsDir() {
		root.Close()
		return "", nil, nil, errors.New("workspace portal root is not a directory")
	}
	pathInfo, err := os.Lstat(canonical)
	if err != nil || !os.SameFile(info, pathInfo) {
		root.Close()
		return "", nil, nil, ErrPortalRootReplaced
	}
	return canonical, root, info, nil
}

func provePortalRootIdentity(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil || expected == nil || !os.SameFile(expected, current) {
		return ErrPortalRootReplaced
	}
	return nil
}
