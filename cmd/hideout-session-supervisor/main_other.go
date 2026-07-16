//go:build !linux

package main

import (
	"errors"
	"io"
)

func runSupervisor(io.Reader, io.Writer) error {
	return errors.New("fixed guest session supervisor is only supported on Linux")
}
