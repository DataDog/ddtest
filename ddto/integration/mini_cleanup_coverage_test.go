package integration

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The SDK reference omits cleanup-only lines. Assert the intended attribution
// directly, including the first-attempt-only policy for retries.
func TestMiniCoverageIncludesCleanup(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{
		"cleanup_only.go": "package fixture\nfunc CleanupOnly() int { return 42 }\n",
		"sample_test.go": `package fixture_test
import("os";"runtime";"testing";fixture "example.com/dd-ci-testing-fixture")
var attempts int
func TestCleanupOnly(t *testing.T) {
 attempts++
 t.Cleanup(func(){ if fixture.CleanupOnly()!=42 { t.Error("cleanup") }; switch os.Getenv("CLEANUP_MODE") { case "cleanup-fail": t.Error("cleanup failure"); case "cleanup-goexit": runtime.Goexit() } })
 switch os.Getenv("CLEANUP_MODE") {
 case "parallel": t.Run("child",func(t *testing.T){t.Parallel();t.Cleanup(func(){fixture.CleanupOnly()})})
 case "retry": if attempts==1 { t.Error("first attempt") }
 case "retry-process": path:=os.Getenv("POC_RETRY_COUNTER");if _,err:=os.Stat(path);os.IsNotExist(err) { if err:=os.WriteFile(path,[]byte("first"),0600);err!=nil {t.Fatal(err)};t.Error("first attempt") }
 case "panic": panic("body panic")
 case "child-panic": t.Run("child",func(t *testing.T){panic("child panic")})
 case "failnow": t.FailNow()
 case "skip": t.Skip("fixture")
 }
}
`,
	})
	bin := filepath.Join(t.TempDir(), executableName("cleanup.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-race", "-covermode=atomic", "-coverpkg=./...", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	for _, deferred := range []bool{false, true} {
		for _, mode := range []string{"pass", "parallel", "retry", "retry-process", "efd", "efd-process", "panic", "child-panic", "failnow", "skip", "cleanup-fail", "cleanup-goexit"} {
			t.Run(fmt.Sprintf("%s/deferred=%t", mode, deferred), func(t *testing.T) {
				retry, efd := strings.HasPrefix(mode, "retry"), strings.HasPrefix(mode, "efd")
				policy := policySettings{Coverage: true, Retry: retry, EFD: efd, Known: efd}
				executionMode := "in_process"
				if strings.HasSuffix(mode, "-process") {
					executionMode = "process"
				}
				got, exec := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestCleanupOnly$", "-test.timeout=10s"}, Policy: policy, Coverage: true, Logs: true, Env: []string{"CLEANUP_MODE=" + mode, fmt.Sprintf("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=%t", retry), fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", efd), fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred), "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + executionMode, "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES=2"}})
				wantExit := 0
				if mode == "panic" || mode == "child-panic" {
					wantExit = 2
				}
				if mode == "failnow" || mode == "cleanup-fail" {
					wantExit = 1
				}
				if exec.code != wantExit {
					t.Fatalf("exit %d want %d: %s\n%s", exec.code, wantExit, exec.out, exec.stderr)
				}
				if retry || efd {
					counts, err := countCIEvents(got.events)
					if err != nil || counts.Tests < 2 {
						t.Fatalf("retry/efd did not execute: %+v %v", counts, err)
					}
				}
				rows := normalizedMiniCoverage(t, &got.miniWireCapture)
				matches := 0
				for _, row := range rows {
					if strings.Contains(row, "cleanup_only.go") {
						matches++
					}
				}
				if matches != 1 {
					t.Fatalf("cleanup coverage rows=%d want 1: %v\n%s", matches, rows, exec.stderr)
				}
			})
		}
	}
}
