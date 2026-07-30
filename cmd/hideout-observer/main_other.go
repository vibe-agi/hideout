//go:build !linux

package main

import (
	"errors"
	"io"
)

func runObserverCommand([]string, io.Reader, io.Writer) error {
	return errors.New("fixed guest observer is only supported on Linux")
}
