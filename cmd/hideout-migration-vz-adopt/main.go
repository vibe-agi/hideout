package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

const (
	maximumExecutorRequestBytes  = 256 << 10
	maximumExecutorResponseBytes = 64 << 10
)

const cloudBoothookPrefix = `#cloud-boothook
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
`

const cloudBoothookSuffix = `/run/hideout-migration-helper/hideout-migration-adopt
/sbin/poweroff -f
`

func adoptionCloudBoothook(request vzexecutor.ExecutionRequest) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	var script strings.Builder
	script.WriteString(cloudBoothookPrefix)
	for _, disk := range request.AttachedDisks {
		identifier, err := disk.BlockDeviceIdentifier()
		if err != nil {
			return "", err
		}
		device := "/dev/disk/by-id/virtio-" + identifier + "-part1"
		fmt.Fprintf(
			&script,
			"hideout_disk_wait=0\n"+
				"while [ ! -b '%s' ]; do\n"+
				"  hideout_disk_wait=$((hideout_disk_wait + 1))\n"+
				"  [ \"$hideout_disk_wait\" -lt 600 ] || exit 70\n"+
				"  /bin/sleep 0.1\n"+
				"done\n"+
				"/bin/mkdir -m 0700 '%s'\n"+
				"/bin/mount -t '%s' -o ro,nodev,nosuid,noexec '%s' '%s'\n",
			device, disk.GuestMountPath, disk.FSType, device, disk.GuestMountPath,
		)
	}
	script.WriteString(cloudBoothookSuffix)
	return script.String(), nil
}

func main() {
	if err := runCLI(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migration.vz_adoption.failed")
		os.Exit(1)
	}
}

func runCLI(args []string, stdin io.Reader, stdout io.Writer) error {
	return runCLIWithCapabilityProbe(
		args, stdin, stdout, validateAdoptionExecutorCapability,
	)
}

func runCLIWithCapabilityProbe(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	capabilityProbe func() error,
) error {
	if len(args) == 1 && args[0] == "--probe" {
		if capabilityProbe == nil || capabilityProbe() != nil {
			return errors.New("VZ adoption executor capability is unavailable")
		}
		probe := vzexecutor.CurrentProbe()
		if err := probe.Validate(); err != nil {
			return err
		}
		return encodeExecutorDocument(stdout, probe)
	}
	if len(args) != 0 || stdin == nil || stdout == nil {
		return errors.New("VZ adoption accepts only a typed stdin request or --probe")
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maximumExecutorRequestBytes+1))
	if err != nil || len(data) == 0 || len(data) > maximumExecutorRequestBytes {
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
	if buffer.Len() > maximumExecutorResponseBytes {
		return errors.New("VZ adoption response size is invalid")
	}
	_, err := io.Copy(writer, &buffer)
	return err
}
