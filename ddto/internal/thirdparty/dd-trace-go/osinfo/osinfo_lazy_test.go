// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package osinfo

import (
	"os"
	"os/exec"
	"testing"
)

// TestOSMetadataLoadHelperProcess exits 0 when no OS metadata was detected
// during package initialization.
func TestOSMetadataLoadHelperProcess(t *testing.T) {
	if os.Getenv("DD_TEST_OSINFO_LOAD_HELPER") != "1" {
		return
	}
	fresh := false
	detectOnce.Do(func() { fresh = true })
	if !fresh {
		os.Exit(3)
	}
	os.Exit(0)
}

// Package initialization detects nothing; the first accessor does.
func TestOSMetadataLoadsOnFirstUse(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestOSMetadataLoadHelperProcess$")
	cmd.Env = append(os.Environ(), "DD_TEST_OSINFO_LOAD_HELPER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("OS metadata was detected during initialization: %v: %s", err, out)
	}
	if OSName() == "" || OSVersion() == "" || KernelName() == "" {
		t.Fatalf("missing OS metadata: name=%q version=%q kernel=%q", OSName(), OSVersion(), KernelName())
	}
}
