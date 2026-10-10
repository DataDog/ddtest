// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// upstreamTestCommand is the pinned SDK's filter, kept to show the difference.
func upstreamTestCommand(args []string) string {
	cmd := filepath.Base(args[0])
	if len(args) > 1 {
		cmd = cmd + " " + strings.Join(args[1:], " ") + " "
	}
	for _, pattern := range []string{`(?si)-test.gocoverdir=(.*)\s`, `(?si)-test.v=(.*)\s`, `(?si)-test.testlogfile=(.*)\s`} {
		cmd = regexp.MustCompile(pattern).ReplaceAllString(cmd, "")
	}
	return strings.TrimSpace(cmd)
}

// Only the volatile flags themselves are removed; later arguments remain.
func TestTestCommandRemovesOnlyVolatileFlags(t *testing.T) {
	binary := filepath.Join("build", "pkg.test")
	for _, tc := range []struct {
		name     string
		args     []string
		want     string
		upstream string
	}{
		{name: "binary only", args: []string{binary}, want: "pkg.test", upstream: "pkg.test"},
		{name: "no volatile flag", args: []string{binary, "-test.paniconexit0", "-test.timeout=10m0s", "-test.v", "-test.run=^TestA$"}, want: "pkg.test -test.paniconexit0 -test.timeout=10m0s -test.v -test.run=^TestA$", upstream: "pkg.test -test.paniconexit0 -test.timeout=10m0s -test.v -test.run=^TestA$"},
		{name: "verbose before run", args: []string{binary, "-test.paniconexit0", "-test.timeout=10m0s", "-test.v=true", "-test.count=1", "-test.run=^TestA$"}, want: "pkg.test -test.paniconexit0 -test.timeout=10m0s -test.count=1 -test.run=^TestA$", upstream: "pkg.test -test.paniconexit0 -test.timeout=10m0s"},
		{name: "cacheable go test", args: []string{binary, "-test.testlogfile=/tmp/go-build1/b001/testlog.txt", "-test.paniconexit0", "-test.gocoverdir=/tmp/cover dir", "-test.timeout=10m0s", "-test.v=test2json"}, want: "pkg.test -test.paniconexit0 -test.timeout=10m0s", upstream: "pkg.test"},
		{name: "go test paths", args: []string{binary, "-test.paniconexit0", "-test.gocoverdir=/tmp/go-build1/b001/gocoverdir", "-test.timeout=10m0s", "-test.count=1", "-test.coverprofile=/tmp/go-build1/b001/_cover_.out", "-test.outputdir=/builds/runner/project", "-test.fuzzcachedir=/cache/fuzz/pkg", "-test.cpuprofile=cpu.out"}, want: "pkg.test -test.paniconexit0 -test.timeout=10m0s -test.count=1 -test.cpuprofile=cpu.out", upstream: "pkg.test -test.paniconexit0"},
		{name: "last argument", args: []string{binary, "-test.run=^TestA$", "-test.v=true"}, want: "pkg.test -test.run=^TestA$", upstream: "pkg.test -test.run=^TestA$"},
		{name: "double dash and case", args: []string{binary, "--test.v=true", "-TEST.TESTLOGFILE=log.txt", "-x"}, want: "pkg.test -x", upstream: "pkg.test -"},
		{name: "values that only mention a flag", args: []string{binary, "-test.run=-test.v=x", "test.v=y"}, want: "pkg.test -test.run=-test.v=x test.v=y", upstream: "pkg.test -test.run="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := testCommand(tc.args); got != tc.want {
				t.Fatalf("testCommand() = %q, want %q", got, tc.want)
			}
			if got := upstreamTestCommand(tc.args); got != tc.upstream {
				t.Fatalf("upstream filter = %q, want %q", got, tc.upstream)
			}
		})
	}
}
