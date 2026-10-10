// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build darwin

package osinfo

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"
)

// readProductVersionSysctl and runSWVers are replaced by tests.
var (
	readProductVersionSysctl = func() (string, error) { return syscall.Sysctl("kern.osproductversion") }
	runSWVers                = func() ([]byte, error) { return exec.Command("sw_vers", "-productVersion").Output() }
)

// macOSProductVersion returns the version that sw_vers -productVersion prints.
// The kern.osproductversion sysctl holds the same value without starting a
// process. SYSTEM_VERSION_COMPAT can change sw_vers' answer, so a process with
// that variable set keeps using sw_vers, as does a failed or empty sysctl.
func macOSProductVersion() (string, bool) {
	if _, compat := env.Lookup("SYSTEM_VERSION_COMPAT"); !compat {
		if version, err := readProductVersionSysctl(); err == nil {
			if version = strings.TrimSpace(version); version != "" {
				return version, true
			}
		}
	}
	out, err := runSWVers()
	if err != nil {
		return "", false
	}
	return string(bytes.Trim(out, "\n")), true
}
