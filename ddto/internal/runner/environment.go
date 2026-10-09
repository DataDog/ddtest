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
}

func readGoEnvironment(ctx context.Context, dir string) (*goEnvironment, error) {
	out, err := goTool(ctx, dir, nil, "env", "-json", "GOMOD", "GOWORK", "GOMODCACHE", "GOVERSION", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH")
	if err != nil {
		return nil, err
	}
	var environment goEnvironment
	if err := json.Unmarshal([]byte(out), &environment); err != nil {
		return nil, fmt.Errorf("read Go environment: %w", err)
	}
	return &environment, nil
}

func supportsGoToolchain(value string) bool {
	// Development distributions report "devel go1.N-<revision> ...".
	fields := strings.Fields(strings.TrimPrefix(value, "devel "))
	return len(fields) != 0 && version.IsValid(fields[0]) && version.Compare(fields[0], minimumGoVersion) >= 0
}
