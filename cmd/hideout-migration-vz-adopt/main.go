package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

const maximumExecutorDocumentBytes = 64 << 10

const cloudBoothook = `#cloud-boothook
#!/bin/sh
set -eu
umask 077
trap '/sbin/poweroff -f' EXIT
/bin/mkdir -p /run/hideout-migration-request
/bin/mkdir -p /run/hideout-migration-helper
/bin/mkdir -p /run/hideout-migration-receipt
/bin/mount -t virtiofs -o ro,nodev,nosuid,noexec hideout-migration-request /run/hideout-migration-request
/bin/mount -t virtiofs -o ro,nodev,nosuid hideout-migration-helper /run/hideout-migration-helper
/bin/mount -t virtiofs -o rw,nodev,nosuid,noexec hideout-migration-receipt /run/hideout-migration-receipt
/run/hideout-migration-helper/hideout-migration-adopt
/sbin/poweroff -f
`

func main() {
	if err := runCLI(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migration.vz_adoption.failed")
		os.Exit(1)
	}
}

func runCLI(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 1 && args[0] == "--probe" {
		probe := vzexecutor.CurrentProbe()
		if err := probe.Validate(); err != nil {
			return err
		}
		return encodeExecutorDocument(stdout, probe)
	}
	if len(args) != 0 || stdin == nil || stdout == nil {
		return errors.New("VZ adoption accepts only a typed stdin request or --probe")
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maximumExecutorDocumentBytes+1))
	if err != nil || len(data) == 0 || len(data) > maximumExecutorDocumentBytes {
		return errors.New("VZ adoption request size is invalid")
	}
	var request vzexecutor.ExecutionRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return errors.New("VZ adoption request JSON is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("VZ adoption request has trailing JSON")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	response, err := runAdoptionExecutor(request)
	if err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return err
	}
	return encodeExecutorDocument(stdout, response)
}

func encodeExecutorDocument(writer io.Writer, value any) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if buffer.Len() > maximumExecutorDocumentBytes {
		return errors.New("VZ adoption response size is invalid")
	}
	_, err := io.Copy(writer, &buffer)
	return err
}
