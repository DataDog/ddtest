package runner

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseRuntimeArgs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args, want []string
		runtime    Runtime
	}{
		{"default", []string{"-count", "1", "./..."}, []string{"-count", "1", "./..."}, Mini},
		{"first", []string{"--runtime=sdk", "-count=1"}, []string{"-count=1"}, SDK},
		{"after-count", []string{"-count", "1", "--runtime=mini"}, []string{"-count", "1"}, Mini},
		{"double-dash-count", []string{"--count", "1", "--runtime=sdk"}, []string{"--count", "1"}, SDK},
		{"separate-value", []string{"-race", "--runtime", "mini", "."}, []string{"-race", "."}, Mini},
		{"after-package", []string{"./...", "--runtime", "sdk", "-count=1"}, []string{"./...", "-count=1"}, SDK},
		{"before-chdir", []string{"--runtime=mini", "-C", "module", "."}, []string{"-C", "module", "."}, Mini},
		{"after-chdir", []string{"-C", "module", "--runtime=sdk", "."}, []string{"-C", "module", "."}, SDK},
		{"last-value", []string{"--runtime=mini", "--runtime", "sdk"}, []string{}, SDK},
		{"flag-value", []string{"-run", "--runtime=sdk", "."}, []string{"-run", "--runtime=sdk", "."}, Mini},
		{"test-flag-value", []string{"--test.run", "--runtime=sdk"}, []string{"--test.run", "--runtime=sdk"}, Mini},
		{"args", []string{"-args", "--runtime=sdk"}, []string{"-args", "--runtime=sdk"}, Mini},
		{"double-args", []string{"--args", "--runtime=sdk"}, []string{"--args", "--runtime=sdk"}, Mini},
		{"terminator", []string{"--", "--runtime=sdk"}, []string{"--", "--runtime=sdk"}, Mini},
		{"custom-value", []string{"-custom", "--runtime=sdk"}, []string{"-custom", "--runtime=sdk"}, Mini},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]string(nil), tc.args...)
			options, got, err := ParseCLIArgs(tc.args)
			gotRuntime := options.Runtime
			if err != nil || gotRuntime != tc.runtime || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q %q %v; want %q %q", gotRuntime, got, err, tc.runtime, tc.want)
			}
			if !reflect.DeepEqual(before, tc.args) {
				t.Fatal("modified caller arguments")
			}
		})
	}
}

func TestParseRuntimeArgsRejectsInvalidValues(t *testing.T) {
	for _, args := range [][]string{
		{"--runtime"}, {"--runtime="}, {"--runtime=unknown"},
		{"-count", "1", "--runtime", "unknown"}, {"--runtime", "-count=1"},
	} {
		if _, _, err := ParseCLIArgs(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
}

func TestParseCLIArgsUploadTimeoutAndCIState(t *testing.T) {
	zip, err := filepath.Abs("state.zip")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		args, want []string
		options    CLIOptions
	}{
		{"defaults", []string{"./..."}, []string{"./..."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute}},
		{"timeout", []string{"--git-upload-timeout=2m30s", "-count=1"}, []string{"-count=1"}, CLIOptions{Runtime: Mini, GitUploadTimeout: 150 * time.Second}},
		{"separate timeout", []string{"-race", "--git-upload-timeout", "0", "."}, []string{"-race", "."}, CLIOptions{Runtime: Mini}},
		{"keep", []string{"--keep-ci-state", "./..."}, []string{"./..."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute, KeepCIState: true}},
		{"keep zip", []string{"-count=1", "--keep-ci-state=state.zip"}, []string{"-count=1"}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute, KeepCIState: true, KeepCIStateZip: zip}},
		{"all", []string{"--runtime=sdk", "--keep-ci-state", "--git-upload-timeout=5s"}, []string{}, CLIOptions{Runtime: SDK, GitUploadTimeout: 5 * time.Second, KeepCIState: true}},
		{"test flag value", []string{"-run", "--keep-ci-state", "."}, []string{"-run", "--keep-ci-state", "."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute}},
		{"args", []string{"-args", "--git-upload-timeout=1s"}, []string{"-args", "--git-upload-timeout=1s"}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute}},
		{"panic hook", []string{"--panic-hook", "-race", "."}, []string{"-race", "."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute, PanicHook: true}},
		{"session per binary", []string{"-count=1", "--session-per-binary", "--panic-hook"}, []string{"-count=1"}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute, PanicHook: true, SessionPerBinary: true}},
		{"test flag named like an option", []string{"-run", "--panic-hook", "."}, []string{"-run", "--panic-hook", "."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute}},
		{"file coverage", []string{"--experimental-file-coverage", "-count=1", "./..."}, []string{"-count=1", "./..."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute, FileCoverage: true}},
		{"parallel file coverage", []string{"-count=1", "--experimental-file-coverage=parallel", "./..."}, []string{"-count=1", "./..."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute, FileCoverage: true, FileCoverageParallel: true}},
		{"test flag named like file coverage", []string{"-run", "--experimental-file-coverage", "."}, []string{"-run", "--experimental-file-coverage", "."}, CLIOptions{Runtime: Mini, GitUploadTimeout: time.Minute}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, got, err := ParseCLIArgs(tc.args)
			if err != nil || options != tc.options || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v %q %v; want %+v %q", options, got, err, tc.options, tc.want)
			}
		})
	}
	for _, args := range [][]string{
		{"--git-upload-timeout"}, {"--git-upload-timeout=soon"}, {"--git-upload-timeout=-1s"},
		{"--keep-ci-state=state.tar"}, {"--keep-ci-state="},
		{"--experimental-file-coverage=serial"}, {"--experimental-file-coverage="},
	} {
		if _, _, err := ParseCLIArgs(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
}

// ddto go test is ddto test, also with -C dir before test, where go accepts it.
func TestTestCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want TestForm
	}{
		{[]string{"test", "-v"}, TestForm{Args: []string{"-v"}, Name: "test"}},
		{[]string{"go", "test", "./..."}, TestForm{Args: []string{"./..."}, Name: "go test"}},
		{[]string{"go", "-C", "sub", "test", "-v"}, TestForm{Args: []string{"-C", "sub", "-v"}, Name: "go -C test"}},
		{[]string{"go", "--C", "sub", "test"}, TestForm{Args: []string{"--C", "sub"}, Name: "go -C test"}},
		{[]string{"go", "-C=sub", "test", "."}, TestForm{Args: []string{"-C=sub", "."}, Name: "go -C test"}},
		{[]string{"go", "--C=sub", "test"}, TestForm{Args: []string{"--C=sub"}, Name: "go -C test"}},
		{[]string{"orchestrion", "go", "test", "-v"}, TestForm{Args: []string{"-v"}, Name: "orchestrion go test", Orchestrion: true}},
		{[]string{"orchestrion", "go", "-C", "sub", "test"}, TestForm{Args: []string{"-C", "sub"}, Name: "orchestrion go -C test", Orchestrion: true}},
		{[]string{"go", "tool", "orchestrion", "go", "test", "-v"}, TestForm{Args: []string{"-v"}, Name: "go tool orchestrion go test", Orchestrion: true, GoTool: true}},
		{[]string{"go", "tool", "orchestrion", "go", "-C=sub", "test"}, TestForm{Args: []string{"-C=sub"}, Name: "go tool orchestrion go -C test", Orchestrion: true, GoTool: true}},
	} {
		if got, ok := TestCommand(tc.args); !ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("TestCommand(%q) = %+v, %t; want %+v", tc.args, got, ok, tc.want)
		}
	}
	for _, args := range [][]string{
		nil, {"go"}, {"go", "build"}, {"go", "vet", "test"}, {"go", "-C"}, {"go", "-C", "test"},
		{"go", "-C", "sub"}, {"-C", "sub", "test"}, {"go", "-x", "test"}, {"orchestrion", "go", "build"},
		{"orchestrion", "test"}, {"go", "tool", "orchestrion", "go"}, {"make", "test"},
	} {
		if got, ok := TestCommand(args); ok {
			t.Errorf("TestCommand(%q) = %+v; want another command", args, got)
		}
	}
}

// DD_CIVISIBILITY_EXPERIMENTAL_FILE_COVERAGE enables file coverage when the
// flag is not given; the flag decides when it is.
func TestFileCoverageEnvironment(t *testing.T) {
	flag := func(args ...string) CLIOptions {
		t.Helper()
		options, _, err := ParseCLIArgs(append(args, "."))
		if err != nil {
			t.Fatal(err)
		}
		return options
	}
	for _, tc := range []struct {
		name                   string
		options                CLIOptions
		value                  string
		fileCoverage, parallel bool
		warns                  bool
	}{
		{"unset", flag(), "", false, false, false},
		{"true", flag(), "true", true, false, false},
		{"1", flag(), "1", true, false, false},
		{"parallel", flag(), "parallel", true, true, false},
		{"false", flag(), "false", false, false, false},
		{"invalid", flag(), "sometimes", false, false, true},
		{"flag over parallel", flag("--experimental-file-coverage"), "parallel", true, false, false},
		{"parallel flag over false", flag("--experimental-file-coverage=parallel"), "false", true, true, false},
	} {
		got, warning := tc.options.WithFileCoverageEnvironment(tc.value)
		if got.FileCoverage != tc.fileCoverage || got.FileCoverageParallel != tc.parallel || (warning != "") != tc.warns {
			t.Errorf("%s: file coverage %t, parallel %t, warning %q", tc.name, got.FileCoverage, got.FileCoverageParallel, warning)
		}
	}
}
