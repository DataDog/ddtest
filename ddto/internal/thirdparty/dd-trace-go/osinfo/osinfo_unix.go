// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build unix

package osinfo

import (
	"bufio"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/xsys/unix"
)

// detect fills the OS metadata. Upstream runs it as the package's init.
func detect() {
	// Change the default values for backwards compatibility on scenarios
	if runtime.GOOS == "linux" {
		osName = "Linux (Unknown Distribution)"
		kernelName = "Linux"
	}

	if runtime.GOOS == "darwin" {
		kernelName = "Darwin"
		version, ok := macOSProductVersion()
		if !ok {
			return
		}

		osVersion = version
	}

	if info, err := unix.ReadKernelInfo(); err == nil {
		kernelName = info.Name
		kernelVersion = info.Version
		kernelRelease = strings.SplitN(info.Release, "-", 2)[0]

		// Backwards compatibility on how data is reported for freebsd
		if runtime.GOOS == "freebsd" {
			osVersion = kernelRelease
		}
	}

	f, err := os.Open("/etc/os-release")
	if err != nil {
		return
	}

	defer f.Close()
	if err := readOSRelease(f); err != nil {
		log.Debug("civisibility: error reading OS release: %v", err)
	}
}

// readOSRelease applies available metadata even if a later read fails, matching
// the SDK's best-effort discovery. Malformed lines do not supply a field value.
func readOSRelease(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "NAME":
			osName = strings.Trim(value, "\"")
		case "VERSION":
			osVersion = strings.Trim(value, "\"")
		case "VERSION_ID":
			if osVersion == "" { // Fallback to VERSION_ID if VERSION is not set
				osVersion = strings.Trim(value, "\"")
			}
		}
	}
	return scanner.Err()
}
