// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package logs

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// TestLogsInitializeHostnameHelperProcess initializes logs in a fresh process
// and fails if any goroutine runs hostname discovery afterwards.
func TestLogsInitializeHostnameHelperProcess(t *testing.T) {
	if os.Getenv("DD_TEST_LOGS_HOSTNAME_HELPER") != "1" {
		return
	}
	Initialize("hostname-service")
	defer Stop()
	expected, _ := os.Hostname()
	if host != expected {
		os.Stderr.WriteString("host " + host + " != os.Hostname " + expected + "\n")
		os.Exit(3)
	}
	stacks := make([]byte, 1<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	if strings.Contains(string(stacks), "/dd-trace-go/hostname.") {
		os.Stderr.Write(stacks)
		os.Exit(4)
	}
	os.Exit(0)
}

// Initialize uses the OS hostname, the value upstream's empty first
// hostname.Get falls back to, without starting the unused probe goroutine
// that goleak checks would report.
func TestInitializeUsesOSHostnameWithoutProbes(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestLogsInitializeHostnameHelperProcess$")
	cmd.Env = append(os.Environ(), "DD_TEST_LOGS_HOSTNAME_HELPER=1", "DD_CIVISIBILITY_LOGS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("logs initialization: %v\n%s", err, out)
	}
}
