// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
)

// Processes that report the CLI's application take distinct, gap-free
// request numbers; the owner's first one is 1, and a number taken after
// others is higher than all of them.
func TestCLITelemetrySequenceAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	app := telemetry.NewSharedApp("cli-runtime", "service", "env", "")
	owner, err := WriteCLITelemetry(dir, app)
	if err != nil {
		t.Fatal(err)
	}
	var handed telemetry.SharedApp
	if err := ReadCLIGitStateFile(dir, CLITelemetryFile, &handed); err != nil || !reflect.DeepEqual(handed, app) {
		t.Fatalf("recorded application %+v, %v", handed, err)
	}

	const processes, requests = 8, 40
	taken := make([][]int64, processes)
	var wg sync.WaitGroup
	for p := range processes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each test process has its own sequence over the same directory.
			seq := &CLITelemetrySequence{dir: filepath.Join(dir, cliTelemetrySeqDir)}
			for range requests {
				taken[p] = append(taken[p], seq.Next())
			}
		}()
	}
	wg.Wait()
	if first := owner.Next(); first != 1 {
		t.Fatalf("app-started took %d", first)
	}
	var all []int64
	for p, numbers := range taken {
		if !slices.IsSorted(numbers) {
			t.Errorf("process %d took decreasing numbers: %v", p, numbers)
		}
		all = append(all, numbers...)
	}
	slices.Sort(all)
	for i, n := range all {
		if n != int64(i+2) {
			t.Fatalf("numbers are not 2..%d without repeats: %v", processes*requests+1, all)
		}
	}
	if last := owner.Next(); last != processes*requests+2 {
		t.Fatalf("app-closing took %d after %d numbers", last, processes*requests+1)
	}
	if owner.failed {
		t.Fatal("the sequence fell back to local numbers")
	}

	// Without its directory, a process keeps numbering its own requests.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if next := owner.Next(); next != processes*requests+3 || !owner.failed {
		t.Fatalf("without the directory: %d, failed %t", next, owner.failed)
	}
}

func TestCLITelemetryFailures(t *testing.T) {
	dir := t.TempDir()
	for _, tag := range []string{"status_code:403", cliTelemetryNetworkFailure, "status_code:403", CLITelemetrySequenceFailure} {
		recordCLITelemetryFailure(dir, tag)
	}
	if err := os.WriteFile(filepath.Join(dir, CLITelemetryFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"status_code:403": 2, cliTelemetryNetworkFailure: 1, CLITelemetrySequenceFailure: 1}
	if got := CLITelemetryFailures(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("failures %v, want %v", got, want)
	}
}

// The owner reserves the number of its final request before that request
// takes its data, so that a fallback is reported in it.
func TestCLITelemetrySequenceReserve(t *testing.T) {
	dir := t.TempDir()
	reports := 0
	seq := &CLITelemetrySequence{dir: dir, report: func() { reports++ }}
	if first := seq.Next(); first != 1 {
		t.Fatalf("first number %d", first)
	}
	seq.Reserve()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if next := seq.Next(); next != 2 || reports != 0 {
		t.Fatalf("reserved number %d, %d reports", next, reports)
	}
	seq.Reserve()
	if reports != 1 {
		t.Fatalf("a fallback while reserving made %d reports", reports)
	}
	if next := seq.Next(); next != 3 || reports != 1 {
		t.Fatalf("number %d after the fallback, %d reports", next, reports)
	}
}

// State files keep working at paths that Windows opens only with a prefix.
func TestCLIStateFilesLongPath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 300 {
		dir = filepath.Join(dir, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteCLIGitStateFile(dir, CLIGitRecordFile, CLIGitRecord{Branch: "long"}); err != nil {
		t.Fatal(err)
	}
	var record CLIGitRecord
	if err := ReadCLIGitStateFile(dir, CLIGitRecordFile, &record); err != nil || record.Branch != "long" {
		t.Fatalf("record %+v, %v", record, err)
	}
	mark := filepath.Join(dir, CLIGitUsedFile)
	if err := createUnlogged(mark); err != nil || !existsUnlogged(mark) {
		t.Fatalf("mark: %v", err)
	}
	if runtime.GOOS == "windows" {
		if prefixed := windowsLongPath(mark); !strings.HasPrefix(prefixed, `\\?\`) {
			t.Fatalf("long path %q opened without the prefix", prefixed)
		}
	}
}

// Long Windows paths get the prefix that os gives them; extended and device
// paths keep their meaning.
func TestExtendedWindowsPath(t *testing.T) {
	long := strings.Repeat("d", 250)
	for _, c := range []struct{ name, want string }{
		{`C:\short`, `C:\short`},
		{`C:\` + long, `\\?\C:\` + long},
		{`C:/` + long + `/x`, `\\?\C:\` + long + `\x`},
		{`\\server\share\` + long, `\\?\UNC\server\share\` + long},
		{`\\.\C:\` + long, `\\.\C:\` + long},
		{`//./C:/` + long, `//./C:/` + long},
		{`\\?\C:\` + long, `\\?\C:\` + long},
		{`\??\C:\` + long, `\??\C:\` + long},
		{long, long},
	} {
		if got := extendedWindowsPath(c.name); got != c.want {
			t.Errorf("%.20q...: got %.30q, want %.30q", c.name, got, c.want)
		}
	}
}
