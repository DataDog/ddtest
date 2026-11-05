package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireReviewNoRetryOwnership(t *testing.T, meta map[string]any) {
	t.Helper()
	for _, key := range []string{"test.is_retry", "test.retry_reason", "test.retry.execution_mode", "test.test_management.attempt_to_fix_passed"} {
		if meta[key] != nil {
			t.Fatalf("excluded declaration inherited %s: %v", key, meta)
		}
	}
}

func reviewTestEvents(receiver *parityReceiver, name string) []map[string]any {
	var result []map[string]any
	for _, event := range receiver.events {
		if event["type"] != "test" {
			continue
		}
		meta := event["content"].(map[string]any)["meta"].(map[string]any)
		if meta["test.name"] == name {
			result = append(result, meta)
		}
	}
	return result
}

// Native is the oracle for panic cancellation and queued-parallel precedence.
// All variants compile the same source; only declaration comments differ. File
// markers prove actual callbacks/admission independently of reports and logs.
func checkMiniTerminalProvenance(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import("testing";"os";"runtime";"strings")
func terminalMark(t *testing.T, key string){f,e:=os.OpenFile(os.Getenv("TERMINAL_ORDER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};defer f.Close();if _,e=f.WriteString(key+"\n");e!=nil{t.Fatal(e)}}
func terminalExit(t *testing.T,mode string){switch {case strings.Contains(mode,"goexit"):runtime.Goexit();case strings.Contains(mode,"skip"):t.SkipNow();case strings.Contains(mode,"fail"):t.FailNow()}}
//ANNOTATIONS
func TestTerminalProvenance(t *testing.T){
 if os.Getenv("TERMINAL_QUEUED")=="true"{t.Run("queued",func(t *testing.T){t.Parallel();if e:=os.WriteFile(os.Getenv("TERMINAL_ADMITTED"),[]byte("admitted"),0600);e!=nil{t.Fatal(e)}})}
 mode:=os.Getenv("TERMINAL_MODE")
 t.Cleanup(func(){terminalMark(t,"first");if strings.HasSuffix(mode,"-final"){terminalExit(t,mode)};panic("cleanup first sentinel")})
 if mode!="one"{t.Cleanup(func(){terminalMark(t,"second");if strings.HasSuffix(mode,"-early"){terminalExit(t,mode)};if strings.HasPrefix(mode,"dynamic"){t.Cleanup(func(){terminalMark(t,"dynamic");panic("cleanup dynamic sentinel")})};panic("cleanup second sentinel")})}
 if os.Getenv("TERMINAL_BODY")=="true"{panic("body sentinel")}
}
func TestTerminalCarrier(t *testing.T){t.Run("row",TestTerminalProvenance)}
`
	bins := map[string]string{}
	for _, variant := range []string{"native", "off", "on"} {
		annotations := ""
		if variant == "on" {
			annotations = "//ddtest:no_auto_retry\n//ddtest:prevent_failure_suppression"
		}
		writeBuildFixture(t, dir, map[string]string{"terminal_provenance_test.go": strings.Replace(source, "//ANNOTATIONS", annotations, 1)})
		bin := filepath.Join(t.TempDir(), executableName("terminal-"+variant+".test"))
		build := driver
		if variant == "native" {
			build = "go"
		}
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), build, exclusionBuildArgs(bin, false)...)
		if code != 0 {
			t.Fatalf("compile %s: %d %s %s", variant, code, out, stderr)
		}
		bins[variant] = bin
	}
	sentinels := []string{"body sentinel", "cleanup first sentinel", "cleanup second sentinel", "cleanup dynamic sentinel"}
	modes := []string{"one", "multiple", "dynamic", "goexit-final", "skip-final", "fail-final", "goexit-early", "skip-early", "fail-early", "dynamic-goexit-final", "dynamic-skip-final", "dynamic-fail-final"}
	for _, name := range []string{"TestTerminalProvenance", "TestTerminalCarrier"} {
		for _, mode := range modes {
			for _, body := range []bool{false, true} {
				for _, queued := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/body=%t/queued=%t", name, mode, body, queued), func(t *testing.T) {
						var native execution
						var nativeOrder string
						var nativeAdmitted bool
						for _, variant := range []string{"native", "off", "on"} {
							t.Run(variant, func(t *testing.T) {
								order, admitted := filepath.Join(t.TempDir(), "order"), filepath.Join(t.TempDir(), "admitted")
								receiver, got := runParityCase(t, dir, bins[variant], parityCase{Args: []string{"-test.run=^" + name + "$", "-test.v"}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "TERMINAL_MODE=" + mode, "TERMINAL_BODY=" + fmt.Sprint(body), "TERMINAL_QUEUED=" + fmt.Sprint(queued), "TERMINAL_ORDER=" + order, "TERMINAL_ADMITTED=" + admitted}})
								raw, err := os.ReadFile(order)
								if err != nil {
									t.Fatalf("cleanup callbacks did not execute: %v, exit%d %s %s", err, got.code, got.out, got.stderr)
								}
								_, err = os.Stat(admitted)
								wasAdmitted := err == nil
								if err != nil && !os.IsNotExist(err) {
									t.Fatal(err)
								}
								if queued && body && wasAdmitted {
									t.Fatal("terminal body admitted queued child")
								}
								if queued && !body && !wasAdmitted {
									t.Fatal("normally returned body did not admit queued child")
								}
								if variant == "native" {
									native = got
									nativeOrder = string(raw)
									nativeAdmitted = wasAdmitted
									return
								}
								// Ordinary SDK events may pre-finalize the body failure before
								// cleanup SkipNow cancels its panic; preserve that existing lifetime.
								wantCode := native.code
								if variant == "off" && body && !queued && (mode == "skip-final" || mode == "dynamic-skip-final") {
									wantCode = 1
								}
								if got.code != wantCode || string(raw) != nativeOrder || wasAdmitted != nativeAdmitted {
									t.Fatalf("%s differs from native: exit%d/%d order%q/%q admitted%t/%t\n%s\n%s", variant, got.code, native.code, raw, nativeOrder, wasAdmitted, nativeAdmitted, got.out, got.stderr)
								}
								output, nativeOutput := got.out+got.stderr, native.out+native.stderr
								for _, sentinel := range sentinels {
									if strings.Contains(output, sentinel) != strings.Contains(nativeOutput, sentinel) {
										t.Fatalf("%s command diagnostic %q differs from native\nMini:%s\nNative:%s", variant, sentinel, output, nativeOutput)
									}
								}
								eventName := name
								if name == "TestTerminalCarrier" {
									eventName += "/row"
								}
								events := reviewTestEvents(receiver, eventName)
								wantEvents := 1
								// A returned ordinary body closes before native cleanup; a later
								// fatal cleanup can terminate the process before its buffered flush.
								if variant == "off" && !body && native.code == 2 {
									wantEvents = 0
								}
								if len(events) != wantEvents {
									t.Fatalf("%s events=%d want%d: %v", eventName, len(events), wantEvents, receiver.events)
								}
								if variant == "off" {
									return
								} // Pinned ordinary event lifetime ends before cleanup.
								meta := events[0]
								requireReviewNoRetryOwnership(t, meta)
								if meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management.prevent_failure_suppression"] != "true" {
									t.Fatalf("missing eager policy: %v", meta)
								}
								diagnostics := fmt.Sprint(meta["error.message"]) + fmt.Sprint(meta["error.stack"])
								for _, sentinel := range sentinels {
									if strings.Contains(diagnostics, sentinel) != strings.Contains(nativeOutput, sentinel) {
										t.Fatalf("eager event diagnostic %q differs from native\nmeta:%v\nNative:%s", sentinel, meta, nativeOutput)
									}
								}
								wantStatus := "fail"
								if native.code == 0 {
									wantStatus = "pass"
									if strings.Contains(mode, "skip") {
										wantStatus = "skip"
									}
								}
								if meta["test.status"] != wantStatus {
									t.Fatalf("eager status=%v want%s: %v", meta["test.status"], wantStatus, meta)
								}
								// Original source identity must survive in the combined stack.
								if strings.Contains(diagnostics, "sentinel") && !strings.Contains(fmt.Sprint(meta["error.stack"]), "terminal_provenance_test.go:") {
									t.Fatalf("lost original panic source: %v", meta)
								}
							})
						}
					})
				}
			}
		}
	}
}

// Selecting a quarantined Testify method uses the existing actual race worker.
// PFS on the native callback must preserve both panics and block suppression.
func checkMiniTerminalWorkerProvenance(t *testing.T) {
	if os.Getenv("PARITY_TEST_MODE") != "race" {
		t.Skip("selected quarantine executes callbacks only in actual race/process isolation")
	}
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)

	source := `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
//ddtest:prevent_failure_suppression
//EXTRA
func TestWorkerPanicCallback(t *testing.T){
 if os.Getenv("WORKER_QUEUED")=="true"{t.Run("queued",func(t *testing.T){t.Parallel();if e:=os.WriteFile(os.Getenv("WORKER_ADMITTED"),[]byte("admitted"),0600);e!=nil{t.Fatal(e)}})}
 t.Cleanup(func(){workerPanicMark(t,"first");panic("worker cleanup first sentinel")})
 if os.Getenv("WORKER_MULTI")=="true"{t.Cleanup(func(){workerPanicMark(t,"second");t.Cleanup(func(){workerPanicMark(t,"dynamic");panic("worker cleanup dynamic sentinel")});panic("worker cleanup second sentinel")})}
 if os.Getenv("WORKER_BODY")=="true"{panic("worker body sentinel")}
}
func workerPanicMark(t *testing.T,key string){f,e:=os.OpenFile(os.Getenv("WORKER_ORDER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};defer f.Close();if _,e=f.WriteString(key+"\n");e!=nil{t.Fatal(e)}}
type WorkerPanicSuite struct{suite.Suite}
func(s *WorkerPanicSuite) TestMethod(){s.T().Run("critical",TestWorkerPanicCallback)}
func TestWorkerPanicSuite(t *testing.T){suite.Run(t,new(WorkerPanicSuite))}
`
	for _, noRetry := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-retry=%t", noRetry), func(t *testing.T) {
			extra := ""
			if noRetry {
				extra = "//ddtest:no_auto_retry\n"
			}
			writeBuildFixture(t, dir, map[string]string{"worker_panic_test.go": strings.Replace(source, "//EXTRA\n", extra, 1)})
			bins := map[string]string{}
			for _, kind := range []string{"native", "Mini"} {
				bin := filepath.Join(t.TempDir(), executableName("worker-panic-"+kind+".test"))
				build := driver
				if kind == "native" {
					build = "go"
				}
				out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), build, exclusionBuildArgs(bin, true)...)
				if code != 0 {
					t.Fatalf("compile%d %s %s", code, out, stderr)
				}
				bins[kind] = bin
			}
			for _, multi := range []bool{false, true} {
				for _, body := range []bool{false, true} {
					for _, queued := range []bool{false, true} {
						t.Run(fmt.Sprintf("multi=%t/body=%t/queued=%t", multi, body, queued), func(t *testing.T) {
							var native execution
							var nativeOrder string
							for _, kind := range []string{"native", "Mini"} {
								order := filepath.Join(t.TempDir(), "order")
								admitted := filepath.Join(t.TempDir(), "admitted")
								receiver, got := runParityCase(t, dir, bins[kind], parityCase{Args: []string{"-test.run=^TestWorkerPanicSuite$/^TestMethod$"}, Policy: policySettings{Quarantined: kind == "Mini", ManagementTarget: "TestWorkerPanicSuite/TestMethod", ManagementSuite: "worker_panic_test.go/WorkerPanicSuite"}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "WORKER_MULTI=" + fmt.Sprint(multi), "WORKER_BODY=" + fmt.Sprint(body), "WORKER_ORDER=" + order, "WORKER_QUEUED=" + fmt.Sprint(queued), "WORKER_ADMITTED=" + admitted}})
								_, admissionErr := os.Stat(admitted)
								if admissionErr != nil && !os.IsNotExist(admissionErr) {
									t.Fatal(admissionErr)
								}
								if queued && ((admissionErr == nil) == body) {
									t.Errorf("%s queued admission=%t body panic=%t %s %s", kind, admissionErr == nil, body, got.out, got.stderr)
								}
								raw, err := os.ReadFile(order)
								if err != nil {
									t.Fatalf("cleanup missing: %v %s %s", err, got.out, got.stderr)
								}
								if kind == "native" {
									native = got
									nativeOrder = string(raw)
									if got.code != 2 {
										t.Fatalf("native panic exit%d %s %s", got.code, got.out, got.stderr)
									}
									continue
								}
								if got.code != 1 || string(raw) != nativeOrder {
									t.Errorf("worker panic failed to block/preserve callback order exit%d order%q native%q %s %s", got.code, raw, nativeOrder, got.out, got.stderr)
								}
								events := reviewTestEvents(receiver, "TestWorkerPanicSuite/TestMethod/critical")
								if len(events) != 1 {
									t.Fatalf("worker callback events%d %v", len(events), receiver.events)
								}
								meta := events[0]
								diag := fmt.Sprint(meta["error.message"]) + fmt.Sprint(meta["error.stack"])
								if meta["test.status"] != "fail" || meta["test.test_management.prevent_failure_suppression"] != "true" || (meta["test.test_management.no_auto_retry"] == "true") != noRetry {
									t.Fatalf("worker callback policy/status %v", meta)
								}
								for _, sentinel := range []string{"worker body sentinel", "worker cleanup first sentinel", "worker cleanup second sentinel", "worker cleanup dynamic sentinel"} {
									want := strings.Contains(native.out+native.stderr, sentinel)
									// The established process protocol sends nested panic text in
									// bounded event fields, without forwarding it to the parent console.
									// Verified against the published runtime before setting this baseline.
									if strings.Contains(diag, sentinel) != want || strings.Contains(got.out+got.stderr, sentinel) {
										t.Errorf("worker diagnostic %q event=%t command=%t native=%t: %v\n%s\n%s\nNative:%s %s", sentinel, strings.Contains(diag, sentinel), strings.Contains(got.out+got.stderr, sentinel), want, meta, got.out, got.stderr, native.out, native.stderr)
									}
								}
								if !strings.Contains(fmt.Sprint(meta["error.stack"]), "worker_panic_test.go:") {
									t.Fatalf("worker original source lost: %v", meta)
								}
							}
						})
					}
				}
			}
		})
	}
}
