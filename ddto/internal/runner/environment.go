package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"go/version"
	"os/exec"
	"strings"
)

const minimumGoVersion = "go1.25.0"

// Go assumes these versions, including their GODEBUG defaults, when go.mod or
// go.work has no go directive.
const (
	defaultGoModVersion  = "1.16"
	defaultGoWorkVersion = "1.18"
)

// crossExecHelper returns the go_$GOOS_$GOARCH_exec program that go test runs
// test binaries with when it cross-compiles and no -exec is given.
func crossExecHelper(environment *goEnvironment) string {
	if environment.GOOS == environment.GOHOSTOS && environment.GOARCH == environment.GOHOSTARCH {
		return ""
	}
	path, err := exec.LookPath("go_" + environment.GOOS + "_" + environment.GOARCH + "_exec")
	if err != nil {
		return ""
	}
	return path
}

// goDebugDefault returns the GODEBUG default=go1.N value for a Go version.
func goDebugDefault(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return ""
	}
	return "go" + strings.Join(parts[:2], ".")
}

type goEnvironment struct {
	GOMOD, GOWORK, GOMODCACHE, GOVERSION string
	GOOS, GOARCH, GOHOSTOS, GOHOSTARCH   string
	// GOEXPERIMENT is the setting as go env reports it, not canonical.
	GOEXPERIMENT string
}

func readGoEnvironment(ctx context.Context, dir string) (*goEnvironment, error) {
	out, err := goTool(ctx, dir, nil, "env", "-json", "GOMOD", "GOWORK", "GOMODCACHE", "GOVERSION", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "GOEXPERIMENT")
	if err != nil {
		return nil, err
	}
	var environment goEnvironment
	if err := json.Unmarshal([]byte(out), &environment); err != nil {
		return nil, fmt.Errorf("read Go environment: %w", err)
	}
	return &environment, nil
}

// testRuntimeVersion returns what runtime.Version() reports in the test
// binaries that the selected toolchain links in dir, for the requests that the
// Git worker makes as they would. goVersion, from go env, is the go command's
// own version, with the toolchain's default experiments. When goExperiment
// sets others, the linker appends their canonical list, which go tool compile
// -V prints, to the toolchain's version. A wrong result only costs the test
// processes their own requests.
func testRuntimeVersion(ctx context.Context, dir, goVersion, goExperiment string) string {
	debug := debugFromContext(ctx)
	if _, own, _ := cutExperiments(goVersion); goExperiment == own {
		debug.printf("test runtime version=%q source=go_env", goVersion)
		return goVersion
	}
	phase := debug.start("test runtime version")
	cmd := exec.CommandContext(ctx, "go", "tool", "compile", "-V")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GOFLAGS=", "GOEXPERIMENT="+goExperiment)
	out, err := cmd.Output()
	phase.finish(err)
	if err != nil {
		debug.printf("test runtime version=%q source=go_env compile_error=true", goVersion)
		return goVersion
	}
	runtimeVersion := strings.TrimPrefix(strings.TrimSpace(string(out)), "compile version ")
	if base, experiments, _ := strings.Cut(runtimeVersion, " X:"); experiments == "" {
		runtimeVersion = base
	} else {
		runtimeVersion = base + experimentSeparator(goVersion, base) + "X:" + experiments
	}
	debug.printf("test runtime version=%q source=go_tool_compile", runtimeVersion)
	return runtimeVersion
}

// cutExperiments splits a runtime version at the experiments that the linker
// appends after "-X:" or " X:".
func cutExperiments(value string) (base, experiments string, found bool) {
	for _, separator := range []string{"-X:", " X:"} {
		if base, experiments, found := strings.Cut(value, separator); found {
			return base, experiments, true
		}
	}
	return value, "", false
}

// experimentSeparator returns what the linker writes before "X:" after base.
// goVersion shows it when the toolchain has default experiments. Otherwise Go
// 1.26 and later write "-" after a version without one (go.dev/issue/75953),
// and earlier versions a space.
func experimentSeparator(goVersion, base string) string {
	if i := strings.Index(goVersion, "X:"); i > 0 {
		return goVersion[i-1 : i]
	}
	if strings.Contains(base, "-") || !version.IsValid(base) || version.Compare(base, "go1.26rc1") < 0 {
		return " "
	}
	return "-"
}

func supportsGoToolchain(value string) bool {
	// Development distributions report "devel go1.N-<revision> ...".
	fields := strings.Fields(strings.TrimPrefix(value, "devel "))
	return len(fields) != 0 && version.IsValid(fields[0]) && version.Compare(fields[0], minimumGoVersion) >= 0
}
