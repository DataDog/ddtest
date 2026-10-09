package runner

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestOrchestrionToolexecRecognition(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{"orchestrion toolexec", true}, {"'/path with spaces/orchestrion' toolexec", true}, {"orchestrion.exe toolexec", true},
		{"go tool orchestrion toolexec", true}, {"'/path/go' tool orchestrion toolexec", true},
		{"env orchestrion toolexec", false}, {"orchestrion go test", false}, {"other toolexec", false}, {"orchestrion toolexec extra", false}, {"'unterminated", false},
	} {
		if got := isOrchestrionToolexec(tc.command); got != tc.want {
			t.Fatalf("%q: %v", tc.command, got)
		}
	}
}

func TestOrchestrionPackageOwnershipAndProbes(t *testing.T) {
	t.Setenv(orchestrionBypassEnv, string(Mini))
	t.Setenv(userToolexecEnv, "orchestrion toolexec")
	for _, tc := range []struct {
		pkg  string
		skip bool
	}{
		{sdkCIEnvironmentPackage, true}, {sdkCIConfigPackage, true}, {"testing", true}, {"testing [client.test]", true}, {"github.com/stretchr/testify/suite", true},
		{miniModule + "/testopt", true}, {miniModule + "/internal/minitracer", true},
		{miniModule + "-other/testopt", false}, {"example.com/client", false}, {"net/http", false},
		{"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer", false}, {"", false},
	} {
		t.Setenv("TOOLEXEC_IMPORTPATH", tc.pkg)
		args := []string{"compile", "input.go"}
		got, err := chainUserToolexec(args)
		if err != nil {
			t.Fatal(err)
		}
		expected := args
		if !tc.skip {
			expected = append([]string{"orchestrion", "toolexec"}, args...)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("%q: %v", tc.pkg, got)
		}
		if bypassOrchestrionPackage([]string{"compile", "-V=full"}) {
			t.Fatal("version probe bypassed Orchestrion")
		}
	}
	t.Setenv(orchestrionBypassEnv, string(SDK))
	for _, pkg := range []string{sdkCIEnvironmentPackage, sdkCIConfigPackage, miniModule, miniModule + "/testopt", "example.com/client"} {
		t.Setenv("TOOLEXEC_IMPORTPATH", pkg)
		if bypassOrchestrionPackage([]string{"compile", "input.go"}) {
			t.Fatal("SDK backend bypassed", pkg)
		}
	}
	for _, pkg := range []string{"testing", "github.com/stretchr/testify/suite"} {
		t.Setenv("TOOLEXEC_IMPORTPATH", pkg)
		if !bypassOrchestrionPackage([]string{"compile", "input.go"}) {
			t.Fatal("ddto-owned package was woven twice", pkg)
		}
	}
	t.Setenv(orchestrionBypassEnv, "")
	t.Setenv("TOOLEXEC_IMPORTPATH", "testing")
	if bypassOrchestrionPackage([]string{"compile", "input.go"}) {
		t.Fatal("generic wrapper was changed")
	}
}

func TestOrchestrionLauncherFlagPriority(t *testing.T) {
	ctx := context.WithValue(context.Background(), goLauncherKey{}, goLauncher{[]string{"orchestrion", "go"}, "orchestrion toolexec"})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "orchestrion toolexec"}, {[]string{"-toolexec=custom"}, "custom"}, {[]string{"-args", "-toolexec=custom"}, "orchestrion toolexec"},
	} {
		opts, err := parseOptions(tc.args, "-toolexec=from-env")
		if err != nil {
			t.Fatal(err)
		}
		applyGoLauncher(ctx, &opts)
		if opts.toolexec != tc.want {
			t.Fatalf("%v: %q", tc.args, opts.toolexec)
		}
	}
}

func TestOrchestrionToolModes(t *testing.T) {
	for _, mode := range []string{"orchestrion", "orchestrion-testify", "orchestrion-goleak", "orchestrion-cover", "orchestrion-testify-goleak-cover", "mini-sdk", "mini-sdk-testify-goleak-cover", "mini-sdk-orchestrion", "mini-sdk-orchestrion-testify-goleak-cover"} {
		if !ValidToolMode(mode) {
			t.Fatal(mode)
		}
	}
	for _, mode := range []string{"", "unknown", "orchestrion-orchestrion", "orchestrion-cover-testify", "mini-testify", "mini-cover", "mini-sdk-sdk", "mini-sdk-orchestrion-orchestrion"} {
		if ValidToolMode(mode) {
			t.Fatal("invalid mode", mode)
		}
	}
	// cmd/go reads an unquoted word with both quote characters, as in an -exec
	// program's argument, so it must survive the wrapper.
	original := []string{"path with spaces/go", `C:\tools\orchestrion`, `PAYLOAD={"s":"don't"}`}
	quoted, err := quoteToolWords(original)
	if err != nil {
		t.Fatal(err)
	}
	words, err := splitFlags(quoted)
	if err != nil || !reflect.DeepEqual(words, original) {
		t.Fatal(words, err)
	}
	for _, word := range []string{`both 'quotes"`, `'starts"`} {
		if _, err := quoteToolWords([]string{word}); err == nil || !strings.Contains(err.Error(), "quote") {
			t.Fatal(word, err)
		}
	}
}

func TestOrchestrionLauncherRecognition(t *testing.T) {
	launcher, ok := orchestrionLauncher("'/path with spaces/orchestrion' toolexec")
	if !ok || !reflect.DeepEqual(launcher.command, []string{"/path with spaces/orchestrion", "go"}) {
		t.Fatal(launcher)
	}
	launcher, ok = orchestrionLauncher("go tool orchestrion toolexec")
	if !ok || !reflect.DeepEqual(launcher.command, []string{"go", "tool", "orchestrion", "go"}) {
		t.Fatal(launcher)
	}
}
