package fixture_test

import (
	"context"
	fixture "example.com/dd-ci-testing-fixture"
	"flag"
	"fmt"
	_ "github.com/DataDog/dd-trace-go/v2/civisibility"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

var mode = flag.String("mode", "", "fixture failure mode")

func TestMain(m *testing.M) {
	flag.Parse()
	fmt.Println("main.before")
	code := m.Run()
	fmt.Println("main.after", code)
	os.Exit(code)
}
func TestPass(t *testing.T) {
	if fixture.Add(2, 3) != 5 {
		t.Fatal("addition")
	}
	t.Log("test.log")
}
func TestSkip(t *testing.T)    { t.Skip("skip reason") }
func TestSkipf(t *testing.T)   { t.Skipf("skip %s", "formatted") }
func TestSkipNow(t *testing.T) { t.SkipNow() }
func TestCleanup(t *testing.T) {
	var order []string
	t.Cleanup(func() {
		if !reflect.DeepEqual(order, []string{"body", "child", "child.cleanup", "after"}) {
			t.Errorf("cleanup order: %v", order)
		}
	})
	order = append(order, "body")
	t.Run("child", func(t *testing.T) {
		order = append(order, "child")
		t.Cleanup(func() { order = append(order, "child.cleanup") })
	})
	order = append(order, "after")
}
func TestContext(t *testing.T) {
	ctx := t.Context()
	t.Cleanup(func() {
		select {
		case <-ctx.Done():
		default:
			t.Error("test context not cancelled before cleanup")
		}
	})
}
func TestParallel(t *testing.T) {
	var done atomic.Int64
	t.Cleanup(func() {
		if done.Load() != 8 {
			t.Error("parallel children missing")
		}
	})
	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) { t.Parallel(); done.Add(1) })
	}
}
func TestNested(t *testing.T) {
	t.Run("a", func(t *testing.T) { t.Run("b", func(t *testing.T) { t.Log("nested") }) })
}

type onceFormatted struct{ calls *atomic.Int64 }

func (v onceFormatted) String() string { v.calls.Add(1); return "formatted once" }
func TestFailures(t *testing.T) {
	if *mode != "fail" {
		t.Skip("explicit negative case")
	}
	for _, name := range []string{"Fail", "FailNow", "Error", "Errorf", "Fatal", "Fatalf"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			v := onceFormatted{&calls}
			if name != "Fail" && name != "FailNow" {
				t.Cleanup(func() {
					if calls.Load() != 1 {
						t.Errorf("formatted %d times", calls.Load())
					}
				})
			}
			switch name {
			case "Fail":
				t.Fail()
			case "FailNow":
				t.FailNow()
			case "Error":
				t.Error(v)
			case "Errorf":
				t.Errorf("message %s", v)
			case "Fatal":
				t.Fatal(v)
			case "Fatalf":
				t.Fatalf("message %s", v)
			}
		})
	}
}
func errorHelper(t *testing.T) { t.Helper(); t.Errorf("helper error") }
func TestHelper(t *testing.T) {
	if *mode == "fail" {
		errorHelper(t)
	}
}
func TestPanic(t *testing.T) {
	if *mode == "panic" {
		panic("fixture panic")
	}
}
func TestGoexit(t *testing.T) {
	if *mode == "goexit" {
		runtime.Goexit()
	}
}
func TestTimeout(t *testing.T) {
	if *mode == "timeout" {
		time.Sleep(time.Second)
	}
}
func TestRetry(t *testing.T) {
	if *mode == "retry" {
		path := os.Getenv("POC_RETRY_COUNTER")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err = os.WriteFile(path, []byte("attempt"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Error("first attempt fails")
		}
	}
}
func BenchmarkWork(b *testing.B) {
	b.Run("child", func(b *testing.B) {
		for b.Loop() {
			_ = fixture.Add(1, 2)
		}
	})
}
func ExampleAdd() {
	fmt.Println(fixture.Add(2, 3)) // Output: 5
}
func FuzzAdd(f *testing.F) {
	f.Add(1, 2)
	f.Fuzz(func(t *testing.T, a, b int) {
		if fixture.Add(a, b) != a+b {
			t.Error("addition")
		}
	})
}

func TestManaged(t *testing.T) {
	if *mode == "managed" {
		t.Error("managed failure")
	}
	if *mode == "fix" {
		path := os.Getenv("POC_RETRY_COUNTER")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err = os.WriteFile(path, []byte("attempt"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Error("first fix attempt fails")
		}
	}
}

// TestCLIEnvironment checks the SDK-visible mode and its inheritance by a real
// child test process. It runs only when selected by the CLI environment suite.
func TestCLIEnvironment(t *testing.T) {
	if *mode != "environment" {
		t.Skip("explicit environment case")
	}
	got, defined := os.LookupEnv("DD_CIVISIBILITY_ENABLED")
	want := os.Getenv("POC_EXPECT_CIVISIBILITY")
	if !defined || got != want {
		t.Fatalf("CI Visibility environment: defined=%t value=%q, want %q", defined, got, want)
	}
	if os.Getenv("POC_ENV_CHILD") == "true" {
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestCLIEnvironment$", "-mode=environment")
	child.Env = append(os.Environ(), "POC_ENV_CHILD=true")
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child environment: %v\n%s", err, out)
	}
}
