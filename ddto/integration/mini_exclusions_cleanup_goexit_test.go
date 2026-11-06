package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func checkMiniExcludedCleanupGoexit(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"cleanup_goexit_test.go": `package fixture_test
import("testing";"os";"runtime")
func terminalCleanupBody(t *testing.T){
 if os.Getenv("TERMINAL_PARALLEL_CHILD")=="true"{
  t.Run("queued",func(t *testing.T){t.Parallel();if err:=os.WriteFile(os.Getenv("TERMINAL_CHILD_ADMITTED"),[]byte("ran"),0600);err!=nil{t.Fatal(err)}})
 }
 t.Cleanup(func(){
  switch os.Getenv("TERMINAL_CLEANUP"){
  case "failnow":t.FailNow()
  case "skipnow":t.SkipNow()
  case "goexit":runtime.Goexit()
  }
 })
 panic("body terminal sentinel")
}
//ddtest:no_auto_retry
func TestNoRetryTerminal(t *testing.T){terminalCleanupBody(t)}
//ddtest:prevent_failure_suppression
func TestPreventTerminal(t *testing.T){terminalCleanupBody(t)}
func TestTerminalCarrier(t *testing.T){
 if os.Getenv("TERMINAL_DECLARATION")=="TestNoRetryTerminal"{t.Run("selected",TestNoRetryTerminal)}else{t.Run("selected",TestPreventTerminal)}
}
func TestTerminalLater(t *testing.T){if err:=os.WriteFile(os.Getenv("TERMINAL_LATER"),[]byte("ran"),0600);err!=nil{t.Fatal(err)}}
`})
	native := filepath.Join(t.TempDir(), executableName("native-goexit.test"))
	out, stderr, code := command(t, dir, testEnv(), "go", exclusionBuildArgs(native, false)...)
	if code != 0 {
		t.Fatalf("native compile: %d %s %s", code, out, stderr)
	}
	mini := filepath.Join(t.TempDir(), executableName("mini-goexit.test"))
	out, stderr, code = command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(mini, false)...)
	if code != 0 {
		t.Fatalf("Mini compile: %d %s %s", code, out, stderr)
	}
	for _, decl := range []struct{ name, annotation string }{{"TestNoRetryTerminal", "no_auto_retry"}, {"TestPreventTerminal", "prevent_failure_suppression"}} {
		for _, scope := range []string{"top", "subtest", "queued-child"} {
			for _, cleanup := range []string{"failnow", "skipnow", "goexit"} {
				for _, target := range []struct{ name, bin string }{{"native", native}, {"Mini", mini}} {
					t.Run(decl.name+"/"+scope+"/"+cleanup+"/"+target.name, func(t *testing.T) {
						selected := decl.name
						if scope == "subtest" {
							selected = "TestTerminalCarrier"
						}
						later := filepath.Join(t.TempDir(), "later")
						admitted := filepath.Join(t.TempDir(), "admitted")
						parallel := "false"
						if scope == "queued-child" {
							parallel = "true"
						}
						receiver, got := runParityCase(t, dir, target.bin, parityCase{
							Args: []string{"-test.run=^(" + selected + "|TestTerminalLater)$"},
							Env:  []string{"TERMINAL_CLEANUP=" + cleanup, "TERMINAL_DECLARATION=" + decl.name, "TERMINAL_LATER=" + later, "TERMINAL_CHILD_ADMITTED=" + admitted, "TERMINAL_PARALLEL_CHILD=" + parallel},
						})
						want := 2
						status := "fail"
						if scope != "queued-child" {
							if cleanup == "failnow" {
								want = 1
							} else if cleanup == "skipnow" {
								want = 0
								status = "skip"
							}
						}
						if got.code != want {
							t.Fatalf("terminal cleanup exit=%d want%d: %s %s", got.code, want, got.out, got.stderr)
						}
						_, err := os.Stat(later)
						if want < 2 {
							if err != nil {
								t.Fatalf("next selected test did not run: %v", err)
							}
						} else if !os.IsNotExist(err) {
							t.Fatalf("next selected test ran after terminal: %v", err)
						}
						if _, err := os.Stat(admitted); !os.IsNotExist(err) {
							t.Fatalf("queued parallel child ran after terminal: %v", err)
						}
						if target.name == "Mini" {
							name := decl.name
							if scope == "subtest" {
								name = "TestTerminalCarrier/selected"
							}
							count := 0
							for _, event := range receiver.events {
								if event["type"] != "test" {
									continue
								}
								meta := event["content"].(map[string]any)["meta"].(map[string]any)
								if meta["test.name"] != name {
									continue
								}
								count++
								if meta["test.status"] != status || meta["test.test_management."+decl.annotation] != "true" {
									t.Fatalf("terminal cleanup event=%v", meta)
								}
							}
							if count != 1 {
								t.Fatalf("terminal cleanup events=%d want1", count)
							}
						}
					})
				}
			}
		}
	}
}
