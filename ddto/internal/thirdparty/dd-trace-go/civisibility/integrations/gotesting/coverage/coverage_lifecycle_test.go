package coverage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
)

// Run the global-zone mutation in a separate process so it cannot race with
// another package test's clock or HTTP receiver.
func TestCoveragePayloadConcurrentLocalChange(t *testing.T) {
	const helperEnv = "DDTEST_COVERAGE_CLOCK_HELPER"
	if os.Getenv(helperEnv) == "1" {
		previous := time.Local
		defer func() { time.Local = previous }()
		zones := []*time.Location{time.FixedZone("A", 3600), time.FixedZone("B", -3600)}
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for i := 0; i < 1000; i++ {
				payload := newCoveragePayload()
				if err := payload.push(&ciTestCoverageData{SpanID: 1}); err != nil {
					t.Error(err)
					return
				}
				if _, err := payload.getBuffer(); err != nil {
					t.Error(err)
					return
				}
				runtime.Gosched()
			}
		}()
		close(start)
		for i := 0; i < 20000; i++ {
			time.Local = zones[i%len(zones)]
			if i%16 == 0 {
				runtime.Gosched()
			}
		}
		workers.Wait()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCoveragePayloadConcurrentLocalChange$", "-test.count=1")
	cmd.Env = append(os.Environ(), helperEnv+"=1", "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false", "DD_TRACE_DEBUG=false")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("coverage payload clock: %v\n%s", err, output)
	}
}

func BenchmarkCoveragePayloadPush(b *testing.B) {
	payload := newCoveragePayload()
	data := &ciTestCoverageData{SpanID: 1}
	b.ReportAllocs()
	for b.Loop() {
		payload.reset()
		if err := payload.push(data); err != nil {
			b.Fatal(err)
		}
	}
}
func writeProcessingProfiles(t *testing.T) *testCoverage {
	t.Helper()
	dir := t.TempDir()
	coverage := &testCoverage{
		testID: 1, sessionID: 2, suiteID: 3, moduleID: 4,
		preCoverageFilename:  filepath.Join(dir, "before.out"),
		postCoverageFilename: filepath.Join(dir, "after.out"),
	}
	for path, count := range map[string]int{coverage.preCoverageFilename: 0, coverage.postCoverageFilename: 1} {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("mode: atomic\nsource.go:1.1,1.20 1 %d\n", count)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	previous := covWriter
	covWriter = &coverageWriter{payload: newCoveragePayload(), climit: make(chan struct{}, concurrentConnectionLimit)}
	t.Cleanup(func() { covWriter = previous })
	return coverage
}

func waitForProcessing(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("coverage processing did not finish before session shutdown")
	}
}

func TestCoverageProcessingFinishesBeforeShutdown(t *testing.T) {
	t.Setenv(cidelivery.DeferredEnv, "false")
	coverage := writeProcessingProfiles(t)
	done := coverage.scheduleProcessing()
	waitForProcessing(t, done)
	// A completion signal must also release later or concurrent shutdown callers.
	waitForProcessing(t, done)
	if covWriter.payload.itemCount() != 1 {
		t.Fatal("coverage was not queued")
	}
	if _, err := os.Stat(coverage.postCoverageFilename); !os.IsNotExist(err) {
		t.Fatalf("processed profile retained: %v", err)
	}
}

func TestDeferredCoverageProcessingWaitsForWholeTestGroup(t *testing.T) {
	t.Setenv(cidelivery.DeferredEnv, "true")
	coverage := writeProcessingProfiles(t)
	first, second := cidelivery.Begin(), cidelivery.Begin()
	t.Cleanup(first)
	t.Cleanup(second)
	done := coverage.scheduleProcessing()
	first()
	select {
	case <-done:
		t.Fatal("coverage processed while another test remained active")
	default:
	}
	if covWriter.payload.itemCount() != 0 {
		t.Fatal("coverage was serialized during a test")
	}
	if _, err := os.Stat(coverage.postCoverageFilename); err != nil {
		t.Fatalf("captured coverage disappeared before checkpoint: %v", err)
	}
	second()
	waitForProcessing(t, done)
	if covWriter.payload.itemCount() != 1 {
		t.Fatal("checkpoint lost coverage")
	}
}

func TestDeferredCoverageProcessingFailureStillCompletes(t *testing.T) {
	t.Setenv(cidelivery.DeferredEnv, "true")
	coverage := writeProcessingProfiles(t)
	if err := os.Remove(coverage.preCoverageFilename); err != nil {
		t.Fatal(err)
	}
	release := cidelivery.Begin()
	t.Cleanup(release)
	done := coverage.scheduleProcessing()
	release()
	waitForProcessing(t, done)
	if covWriter.payload.itemCount() != 0 {
		t.Fatal("invalid coverage was queued")
	}
}
