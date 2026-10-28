// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A test's log line longer than bufio.Scanner's default limit reaches the
// logs, and so do the lines after it. Logs enablement is cached per process,
// so the check runs in a child with the logs enabled.
func TestCollectLogsKeepsLongLines(t *testing.T) {
	if os.Getenv("DD_TEST_COLLECT_LOGS_HELPER") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCollectLogsKeepsLongLines$")
		cmd.Env = append(os.Environ(), "DD_TEST_COLLECT_LOGS_HELPER=1", "DD_CIVISIBILITY_LOGS_ENABLED=true")
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "PASS") {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	long := strings.Repeat("x", 70_000)
	test := newProcessRetryRecordingTestForTesting("TestLong")
	collectAndWriteLogs(t, test, []byte(long+"\nsentinel\n"))
	if len(test.logs) != 2 || test.logs[0] != long || test.logs[1] != "sentinel" {
		t.Fatalf("logged %d lines", len(test.logs))
	}
}
