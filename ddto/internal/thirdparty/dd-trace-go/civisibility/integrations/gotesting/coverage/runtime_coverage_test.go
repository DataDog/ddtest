// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var runtimeCoverageTestMain *testing.M

func TestMain(m *testing.M) {
	runtimeCoverageTestMain = m
	os.Exit(m.Run())
}

func initializeRuntimeCoverageForTest(t *testing.T) {
	t.Helper()
	if testing.CoverMode() != "atomic" {
		t.Skip("requires -covermode=atomic (enabled in core CI)")
	}
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	// Keep the real Go emitter without starting a coverage uploader.
	InitializeCoverage(runtimeCoverageTestMain, false)
	if mode != "atomic" || tearDown == nil {
		t.Fatal("runtime coverage was not initialized")
	}
	temporaryDir = t.TempDir()
	coverageUploadEnabled = true
	profileFlag := flag.Lookup("test.coverprofile")
	oldProfile := profileFlag.Value.String()
	if err := profileFlag.Value.Set(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := profileFlag.Value.Set(oldProfile); err != nil {
			t.Error(err)
		}
	})
}

func TestRuntimeCoverageConcurrentSnapshots(t *testing.T) {
	initializeRuntimeCoverageForTest(t)
	const workers = 8
	const iterations = 4
	collectors := make([]*testCoverage, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range collectors {
		collector := &testCoverage{moduleID: 1, suiteID: 2, testID: uint64(i + 1)}
		collectors[i] = collector
		wg.Go(func() {
			<-start
			for i := 0; i < iterations; i++ {
				collector.CollectCoverageBeforeTestExecution()
				if err := collector.getCoverageData(); err != nil {
					t.Errorf("collect coverage after test: %v", err)
					return
				}
			}
		})
	}
	wg.Go(func() {
		<-start
		if _, err := RuntimeCoverageSnapshot(); err != nil {
			t.Errorf("collect session coverage: %v", err)
		}
	})
	wg.Go(func() {
		<-start
		for i := 0; i < iterations; i++ {
			profile, err := snapshotProcessCoverageProfile()
			if err != nil {
				t.Errorf("collect process coverage: %v", err)
				return
			}
			if len(profile.lines) <= 1 || profile.lines[0].raw != "mode: atomic" {
				t.Error("process coverage profile is empty or has an unexpected mode")
			}
		}
	})
	close(start)
	wg.Wait()

	for _, collector := range collectors {
		assertRuntimeCoverageProfile(t, collector.preCoverageFilename)
		assertRuntimeCoverageProfile(t, collector.postCoverageFilename)
	}
	if runtimeSnapshot == nil {
		t.Fatal("session coverage snapshot is missing")
	}
	assertRuntimeCoverageProfile(t, runtimeSnapshot.path)
}

func TestRuntimeCoverageSnapshotAfterError(t *testing.T) {
	initializeRuntimeCoverageForTest(t)
	invalidPath := filepath.Join(temporaryDir, "missing", "coverage.out")
	if _, err := tearDown(invalidPath, ""); err == nil {
		t.Fatal("expected an error for a profile in a missing directory")
	}
	profilePath := filepath.Join(temporaryDir, "coverage.out")
	if _, err := tearDown(profilePath, ""); err != nil {
		t.Fatal(err)
	}
	assertRuntimeCoverageProfile(t, profilePath)
}

func assertRuntimeCoverageProfile(t *testing.T, path string) {
	t.Helper()
	profile, err := parseOrderedCoverProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.lines) <= 1 || profile.lines[0].raw != "mode: atomic" {
		t.Fatalf("coverage profile %q is empty or has an unexpected mode", path)
	}
}

// With -test.list, testing runs no test and writes no profile: a file at
// -test.coverprofile is an earlier run's, not this one's coverage.
func TestRuntimeCoverageSnapshotIgnoresProfileWhenListing(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	mode, tearDown = "count", func(string, string) (string, error) { return "", nil }
	profile := filepath.Join(t.TempDir(), "previous.out")
	if err := os.WriteFile(profile, []byte("mode: count\nexample.com/a/a.go:1.1,2.1 1 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"test.coverprofile": profile, "test.list": "."} {
		f := flag.Lookup(name)
		old := f.Value.String()
		if err := f.Value.Set(value); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Value.Set(old) })
	}
	if snapshot, err := RuntimeCoverageSnapshot(); err == nil {
		t.Fatalf("snapshot of an earlier run's profile: %v", snapshot.path)
	}
}

// A -test.coverprofile that is not a regular file, such as /dev/stdout, is not
// read back: reading a pipe that this process holds open waits forever.
func TestRuntimeCoverageSnapshotIgnoresIrregularProfile(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	mode, tearDown = "count", func(string, string) (string, error) { return "", nil }
	f := flag.Lookup("test.coverprofile")
	old := f.Value.String()
	if err := f.Value.Set(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Value.Set(old) })
	if snapshot, err := RuntimeCoverageSnapshot(); err == nil {
		t.Fatalf("snapshot of a directory: %v", snapshot.path)
	}
}

// Without the temporary directory that initialization creates, a text-profile
// capture writes nothing, instead of a relative file in the test's directory.
func TestTextProfileCaptureWithoutTemporaryDirectory(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	var captured []string
	mode, tearDown = "count", func(path, _ string) (string, error) {
		captured = append(captured, path)
		return path, nil
	}
	temporaryDir = ""
	coverage := &testCoverage{moduleID: 1, suiteID: 2, testID: 3}
	if err := coverage.collectCoverageBeforeTestExecution(); !errors.Is(err, errNoCoverageDirectory) {
		t.Fatalf("pre-test capture: %v", err)
	}
	if err := coverage.collectCoverageAfterTestExecution(); !errors.Is(err, errNoCoverageDirectory) {
		t.Fatalf("post-test capture: %v", err)
	}
	if len(captured) != 0 {
		t.Fatalf("captured %q", captured)
	}
}
