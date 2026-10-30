package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The instrumented SDK that ddtest instrument bazel writes works as GOROOT for
// cmd/go too, which compiles the standard library the way rules_go's stdlib
// action does. A test without Mini runs as with the official SDK, and a test
// that links Mini reports its events in Bazel's payload files, with and
// without the panic hook.
func TestBazelCompilerInstrumentsTheSDK(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		path := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	replace := "\nrequire github.com/DataDog/ddtest/ddto v0.0.0\nreplace github.com/DataDog/ddtest/ddto => " + filepath.ToSlash(root) + "\n"
	// The executable that ddtest builds: ddto's CLI under the instrument command.
	write("cli/go.mod", "module example.com/cli\n\ngo 1.25.0\n"+replace)
	write("cli/main.go", "package main\n\nimport (\n\t\"os\"\n\n\t\"github.com/DataDog/ddtest/ddto/cli\"\n)\n\nfunc main() { cli.Main(os.Args, \"instrument\") }\n")
	executable := filepath.Join(work, "bin", executableName("ddtest"))
	if out, stderr, code := command(t, filepath.Join(work, "cli"), testEnv(), "go", "build", "-o", executable, "."); code != 0 {
		t.Fatal(out, stderr)
	}
	goroot, stderr, code := command(t, work, testEnv(), "go", "env", "GOROOT")
	if code != 0 {
		t.Fatal(stderr)
	}
	sdk, panicSDK := filepath.Join(work, "sdk"), filepath.Join(work, "panic-sdk")
	for dir, flags := range map[string][]string{sdk: nil, panicSDK: {"--panic-hook"}} {
		args := append([]string{"instrument", "bazel", "--goroot=" + strings.TrimSpace(goroot), "--compiler=" + executable}, flags...)
		if out, stderr, code := command(t, work, testEnv(), executable, append(args, dir)...); code != 0 {
			t.Fatal(out, stderr)
		}
	}
	// It runs no Bazel command.
	if out, stderr, code := command(t, work, testEnv(), executable, "instrument", "bazel", "test", "//..."); code != 2 || !strings.Contains(stderr, "runs no Bazel command") {
		t.Fatalf("a Bazel command was accepted: exit=%d\n%s\n%s", code, out, stderr)
	}
	const tests = "package fixture\n\nimport \"testing\"\n\nfunc TestPass(t *testing.T) {}\n\nfunc TestFail(t *testing.T) { t.Errorf(\"failed with %d\", 1) }\n\nfunc TestSkip(t *testing.T) { t.Skip(\"skipped\") }\n\nfunc TestSub(t *testing.T) { t.Run(\"a\", func(t *testing.T) {}) }\n"
	write("plain/go.mod", "module example.com/plain\n\ngo 1.25.0\n")
	write("plain/fixture_test.go", tests)
	write("mini/go.mod", "module example.com/mini\n\ngo 1.25.0\n"+replace)
	write("mini/fixture_test.go", tests)
	const panics = "package fixture\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestStartsLatePanic(t *testing.T) {\n\tgo func() {\n\t\ttime.Sleep(200 * time.Millisecond)\n\t\tpanic(\"late panic\")\n\t}()\n}\n\nfunc TestRunsMeanwhile(t *testing.T) { time.Sleep(2 * time.Second) }\n"
	const registration = "package fixture\n\nimport __dd_ci_runtime \"github.com/DataDog/ddtest/ddto/testopt\"\n\nfunc init() { __dd_ci_runtime.RegisterTestPackage() }\n"
	write("mini/zz_dd_ci_visibility_test.go", registration)
	write("plainpanic/go.mod", "module example.com/plainpanic\n\ngo 1.25.0\n")
	write("plainpanic/fixture_test.go", panics)
	write("plaincrash/go.mod", "module example.com/plaincrash\n\ngo 1.25.0\n")
	write("plaincrash/fixture_test.go", "package fixture\n\nimport (\n\t\"os\"\n\t\"runtime/debug\"\n\t\"sync\"\n\t\"testing\"\n)\n\nfunc TestFatalError(t *testing.T) {\n\tfile, err := os.Create(os.Getenv(\"CRASH_OUTPUT\"))\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tvar mu sync.Mutex\n\tmu.Unlock()\n}\n")
	write("minipanic/go.mod", "module example.com/minipanic\n\ngo 1.25.0\n"+replace)
	write("minipanic/fixture_test.go", panics)
	write("minipanic/zz_dd_ci_visibility_test.go", registration)
	// The instrumented compiler shares the official one's identity, so each
	// SDK gets a Go cache of its own, as each Bazel action has.
	sdkEnv := func(sdk string, extra ...string) []string {
		env := testEnv(append([]string{"GOROOT=" + sdk, "GOCACHE=" + sdk + ".gocache", "GOTOOLCHAIN=local"}, extra...)...)
		for i, entry := range env {
			if strings.HasPrefix(entry, "PATH=") {
				env[i] = "PATH=" + filepath.Join(sdk, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
			}
		}
		return env
	}
	// rules_go builds the standard library as go install std does with
	// installgoroot=all, so that other compilations name its archives in
	// GOROOT; the edits rely on that to add a standard import to testing.
	for _, dir := range []string{sdk, panicSDK} {
		if out, stderr, code := command(t, work, sdkEnv(dir, "GODEBUG=installgoroot=all", "GO111MODULE=off", "GOFLAGS="), "go", "install", "std"); code != 0 {
			t.Fatalf("install std with the instrumented compiler: %s\n%s", out, stderr)
		}
	}
	// Timings, and the addresses, goroutine numbers and uncertain argument
	// values (a ? after the value) of a crash, vary.
	volatile := regexp.MustCompile(`\(\d+\.\d+s\)|\t\d+\.\d+s|\+0x[0-9a-f]+|goroutine \d+|0x[0-9a-f]+\??`)
	// Each SDK names its own GOROOT in stack traces.
	normalize := func(sdk, output string) string {
		output = strings.ReplaceAll(output, filepath.ToSlash(sdk), "GOROOT")
		output = strings.ReplaceAll(output, filepath.ToSlash(strings.TrimSpace(goroot)), "GOROOT")
		return volatile.ReplaceAllString(output, "")
	}
	plainTest := func(sdk, pkg string) {
		t.Helper()
		crash := func(name string) string { return filepath.Join(work, pkg+"-"+name+".crash") }
		native, _, nativeCode := command(t, filepath.Join(work, pkg), testEnv("GOFLAGS=-buildvcs=false -count=1", "CRASH_OUTPUT="+crash("native")), "go", "test", "-v", ".")
		edited, stderr, editedCode := command(t, filepath.Join(work, pkg), sdkEnv(sdk, "GOFLAGS=-buildvcs=false -count=1", "CRASH_OUTPUT="+crash(filepath.Base(sdk))), "go", "test", "-v", ".")
		if nativeCode != 1 || editedCode != nativeCode || normalize(sdk, edited) != normalize(sdk, native) {
			t.Fatalf("a test without Mini changed under %s: native exit %d\n%s\nedited exit %d\n%s\n%s", filepath.Base(sdk), nativeCode, native, editedCode, edited, stderr)
		}
		if nativeCrash, err := os.ReadFile(crash("native")); err == nil {
			editedCrash, err := os.ReadFile(crash(filepath.Base(sdk)))
			if err != nil || normalize(sdk, string(editedCrash)) != normalize(sdk, string(nativeCrash)) {
				t.Fatalf("the crash output changed under %s: %v\nnative:\n%s\nedited:\n%s", filepath.Base(sdk), err, nativeCrash, editedCrash)
			}
		}
	}
	miniTest := func(sdk, pkg string, want map[string]string) string {
		t.Helper()
		outputs := filepath.Join(work, pkg+"-outputs")
		out, stderr, code := command(t, filepath.Join(work, pkg), sdkEnv(sdk, "GOFLAGS=-buildvcs=false -mod=mod -count=1", "DD_CIVISIBILITY_ENABLED=true", "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true", "TEST_UNDECLARED_OUTPUTS_DIR="+outputs), "go", "test", "-v", ".")
		if code != 1 {
			t.Fatalf("%s with Mini: exit %d\n%s\n%s", pkg, code, out, stderr)
		}
		payloads, err := filepath.Glob(filepath.Join(outputs, "payloads", "tests", "*.json"))
		if err != nil || len(payloads) == 0 {
			t.Fatalf("%s: no test payload files: %v\n%s\n%s", pkg, err, out, stderr)
		}
		statuses := map[string]string{}
		for _, payload := range payloads {
			data, err := os.ReadFile(payload)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Events []struct {
					Type    string
					Content struct{ Meta map[string]string }
				}
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			for _, event := range decoded.Events {
				if event.Type == "test" {
					statuses[event.Content.Meta["test.name"]] = event.Content.Meta["test.status"]
				}
			}
		}
		if !reflect.DeepEqual(statuses, want) {
			t.Fatalf("%s/%s %s: test statuses %v, want %v\n%s", runtime.GOOS, runtime.GOARCH, pkg, statuses, want, out)
		}
		return out
	}
	plainTest(sdk, "plain")
	miniTest(sdk, "mini", map[string]string{"TestPass": "pass", "TestFail": "fail", "TestSkip": "skip", "TestSub": "pass", "TestSub/a": "pass"})
	// With --panic-hook, a panic and a fatal error keep their native output,
	// also in the crash output, without Mini; with Mini the test that was
	// running when the panic happened fails.
	plainTest(panicSDK, "plain")
	plainTest(panicSDK, "plainpanic")
	plainTest(panicSDK, "plaincrash")
	out := miniTest(panicSDK, "minipanic", map[string]string{"TestStartsLatePanic": "pass", "TestRunsMeanwhile": "fail"})
	if !strings.Contains(out, "panic: late panic") {
		t.Fatalf("the crash's output:\n%s", out)
	}
}
