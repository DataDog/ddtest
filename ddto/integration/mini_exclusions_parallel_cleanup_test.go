package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMiniExcludedParallelCleanup(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"parallel_cleanup_test.go": `package fixture_test
import("testing";"os";"runtime";"sync")
func parallelExcludedCleanup(t *testing.T){
 t.Parallel()
 t.Run("parallel-child",func(t *testing.T){t.Parallel()})
 t.Cleanup(func(){
  switch os.Getenv("CLEANUP_OPERATION"){
  case "setenv": t.Setenv("DDTEST_EXCLUDED_PARALLEL_CLEANUP","changed")
  case "chdir": t.Chdir(os.Getenv("CLEANUP_TARGET_DIR"))
  }
  if err:=os.WriteFile(os.Getenv("CLEANUP_RETURNED"),[]byte("returned"),0600);err!=nil{t.Fatal(err)}
 })
}
//ddtest:no_auto_retry
func TestParallelNoRetry(t *testing.T){parallelExcludedCleanup(t)}
//ddtest:prevent_failure_suppression
func TestParallelPrevent(t *testing.T){parallelExcludedCleanup(t)}
var cleanupLeafDone=make(chan struct{})
var cleanupBlockerActive=make(chan struct{})
var cleanupParentDone=make(chan struct{})
var cleanupRunReturned=make(chan struct{})
var cleanupBlockerOnce sync.Once
//ddtest:no_auto_retry
func TestCleanupSchedulerA(t *testing.T){
 t.Parallel()
 t.Run("sequential",func(t *testing.T){
  t.Run("leaf",func(t *testing.T){
   t.Parallel()
   if os.Getenv("CLEANUP_SCHEDULER_PANIC")=="body-panic"{
    if err:=os.WriteFile(os.Getenv("CLEANUP_CHILD_ADMITTED"),[]byte("ran"),0600);err!=nil{t.Fatal(err)}
   }
   t.Cleanup(func(){
    close(cleanupLeafDone)
    <-cleanupBlockerActive
    for i:=0;i<10;i++{runtime.Gosched()}
   })
  })
  t.Cleanup(func(){
   switch os.Getenv("CLEANUP_SCHEDULER_PANIC"){
   case "panic": panic("native sequential cleanup panic")
   case "skip": t.SkipNow()
   case "failnow": t.FailNow()
   case "goexit": runtime.Goexit()
   }
   close(cleanupParentDone)
  })
  if os.Getenv("CLEANUP_SCHEDULER_PANIC")=="body-panic"{panic("native sequential body panic")}
 })
 close(cleanupRunReturned)
}
func TestCleanupSchedulerB(t *testing.T){
 t.Parallel()
 <-cleanupLeafDone
 for _,name:=range []string{"first","second"} {
  t.Run(name,func(t *testing.T){
   t.Parallel()
   cleanupBlockerOnce.Do(func(){close(cleanupBlockerActive)})
   mode:=os.Getenv("CLEANUP_SCHEDULER_PANIC")
   if mode=="skip"||mode=="failnow"||mode=="goexit"{<-cleanupRunReturned}else{<-cleanupParentDone}
  })
 }
}
`})
	native := filepath.Join(t.TempDir(), executableName("native-parallel-cleanup.test"))
	out, stderr, code := command(t, dir, testEnv(), "go", exclusionBuildArgs(native, false)...)
	if code != 0 {
		t.Fatalf("native compile: %d %s %s", code, out, stderr)
	}
	mini := filepath.Join(t.TempDir(), executableName("mini-parallel-cleanup.test"))
	out, stderr, code = command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(mini, false)...)
	if code != 0 {
		t.Fatalf("Mini compile: %d %s %s", code, out, stderr)
	}
	for _, tc := range []struct{ name, annotation string }{
		{"TestParallelNoRetry", "no_auto_retry"},
		{"TestParallelPrevent", "prevent_failure_suppression"},
	} {
		for _, operation := range []string{"setenv", "chdir", "ok"} {
			for _, target := range []struct{ name, bin string }{{"native", native}, {"Mini", mini}} {
				t.Run(tc.name+"/"+operation+"/"+target.name, func(t *testing.T) {
					returned := filepath.Join(t.TempDir(), "returned")
					receiver, got := runParityCase(t, dir, target.bin, parityCase{
						Args: []string{"-test.run=^" + tc.name + "$", "-test.parallel=1"},
						Env:  []string{"CLEANUP_OPERATION=" + operation, "CLEANUP_TARGET_DIR=" + t.TempDir(), "CLEANUP_RETURNED=" + returned},
					})
					want := 2
					if operation == "ok" {
						want = 0
					}
					if got.code != want {
						t.Fatalf("cleanup exit=%d want%d: %s %s", got.code, want, got.out, got.stderr)
					}
					_, err := os.Stat(returned)
					if operation == "ok" {
						if err != nil {
							t.Fatalf("ordinary parallel cleanup did not finish: %v", err)
						}
					} else {
						if !os.IsNotExist(err) {
							t.Fatalf("parallel cleanup escaped native guard: %v", err)
						}
						if !strings.Contains(got.stderr, "can not use t.Parallel") {
							t.Fatalf("native parallel guard missing: %s", got.stderr)
						}
					}
					if target.name == "Mini" {
						count := 0
						for _, event := range receiver.events {
							if event["type"] != "test" {
								continue
							}
							meta := event["content"].(map[string]any)["meta"].(map[string]any)
							if meta["test.name"] != tc.name {
								continue
							}
							count++
							status := "fail"
							if operation == "ok" {
								status = "pass"
							}
							if meta["test.status"] != status || meta["test.test_management."+tc.annotation] != "true" {
								t.Fatalf("parallel cleanup event=%v", meta)
							}
						}
						if count != 1 {
							t.Fatalf("parent cleanup events=%d want1", count)
						}
					}
				})
			}
		}
	}
	for _, mode := range []string{"pass", "panic", "body-panic", "skip", "failnow", "goexit"} {
		for _, target := range []struct{ name, bin string }{{"native", native}, {"Mini", mini}} {
			t.Run("sequential-cleanup-order/"+mode+"/"+target.name, func(t *testing.T) {
				admitted := filepath.Join(t.TempDir(), "admitted")
				receiver, got := runParityCase(t, dir, target.bin, parityCase{
					Args: []string{"-test.run=^TestCleanupScheduler(A|B)$", "-test.parallel=2", "-test.timeout=10s"},
					Env:  []string{"GOMAXPROCS=1", "CLEANUP_SCHEDULER_PANIC=" + mode, "CLEANUP_CHILD_ADMITTED=" + admitted},
				})
				want := 0
				if mode == "panic" || mode == "body-panic" {
					want = 2
				} else if mode == "failnow" {
					want = 1
				}
				if got.code != want {
					t.Fatalf("native cleanup scheduling exit=%d want%d: %s %s", got.code, want, got.out, got.stderr)
				}
				if mode == "panic" && !strings.Contains(got.stderr, "native sequential cleanup panic") {
					t.Fatalf("native terminal cleanup panic missing: %s", got.stderr)
				}
				if target.name == "Mini" && (mode == "skip" || mode == "failnow" || mode == "goexit") {
					status := "pass"
					if mode == "skip" {
						status = "skip"
					} else if mode == "failnow" {
						status = "fail"
					}
					count := 0
					for _, event := range receiver.events {
						if event["type"] != "test" {
							continue
						}
						meta := event["content"].(map[string]any)["meta"].(map[string]any)
						if meta["test.name"] == "TestCleanupSchedulerA/sequential" {
							count++
							if meta["test.status"] != status || meta["test.test_management.no_auto_retry"] != "true" {
								t.Fatalf("cleanup Goexit event=%v want%s", meta, status)
							}
						}
					}
					if count != 1 {
						t.Fatalf("cleanup Goexit events=%d want1", count)
					}
				}
				if mode == "body-panic" {
					if !strings.Contains(got.stderr, "native sequential body panic") {
						t.Fatalf("native terminal body panic missing: %s", got.stderr)
					}
					if _, err := os.Stat(admitted); !os.IsNotExist(err) {
						t.Fatalf("parallel child ran after terminal parent body panic: %v", err)
					}
				}
			})
		}
	}
}
