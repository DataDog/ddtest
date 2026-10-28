// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build darwin

package osinfo

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func stubMacOSVersionSources(t *testing.T, sysctl func() (string, error), swVers func() ([]byte, error)) {
	t.Helper()
	originalSysctl, originalSWVers := readProductVersionSysctl, runSWVers
	readProductVersionSysctl, runSWVers = sysctl, swVers
	t.Cleanup(func() { readProductVersionSysctl, runSWVers = originalSysctl, originalSWVers })
}

func TestMacOSProductVersionSources(t *testing.T) {
	sysctlVersion := func() (string, error) { return "26.6.2\n", nil }
	swVersVersion := func() ([]byte, error) { return []byte("10.16\n"), nil }
	failure := errors.New("unavailable")
	for _, tc := range []struct {
		name    string
		compat  bool
		sysctl  func() (string, error)
		swVers  func() ([]byte, error)
		version string
		ok      bool
	}{
		{name: "sysctl", sysctl: sysctlVersion, version: "26.6.2", ok: true},
		{name: "compat keeps sw_vers", compat: true, swVers: swVersVersion, version: "10.16", ok: true},
		{name: "sysctl error", sysctl: func() (string, error) { return "", failure }, swVers: swVersVersion, version: "10.16", ok: true},
		{name: "empty sysctl", sysctl: func() (string, error) { return " ", nil }, swVers: swVersVersion, version: "10.16", ok: true},
		{name: "no source", sysctl: func() (string, error) { return "", failure }, swVers: func() ([]byte, error) { return nil, failure }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.compat {
				t.Setenv("SYSTEM_VERSION_COMPAT", "1")
			} else {
				unsetEnv(t, "SYSTEM_VERSION_COMPAT")
			}
			sysctl := tc.sysctl
			if sysctl == nil {
				sysctl = func() (string, error) { t.Fatal("sysctl read with SYSTEM_VERSION_COMPAT set"); return "", nil }
			}
			swVers := tc.swVers
			if swVers == nil {
				swVers = func() ([]byte, error) { t.Fatal("sw_vers started although the sysctl answered"); return nil, nil }
			}
			stubMacOSVersionSources(t, sysctl, swVers)
			if version, ok := macOSProductVersion(); version != tc.version || ok != tc.ok {
				t.Fatalf("macOSProductVersion() = %q, %t; want %q, %t", version, ok, tc.version, tc.ok)
			}
		})
	}
}

// The sysctl reports what sw_vers -productVersion prints on this host.
func TestMacOSProductVersionMatchesSWVers(t *testing.T) {
	unsetEnv(t, "SYSTEM_VERSION_COMPAT")
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		t.Skipf("sw_vers unavailable: %v", err)
	}
	version, ok := macOSProductVersion()
	if want := string(bytes.Trim(out, "\n")); !ok || version != want {
		t.Fatalf("macOSProductVersion() = %q, %t; sw_vers printed %q", version, ok, want)
	}
	if OSVersion() != version {
		t.Fatalf("OSVersion() = %q, want %q", OSVersion(), version)
	}
}

// unsetEnv removes a variable for one test and restores it afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}
