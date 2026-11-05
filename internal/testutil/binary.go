// Package testutil shares expensive fixtures between the CLI integration tests.
package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type binaryCache struct {
	once sync.Once
	path string
	err  error
}

var compiled = new(binaryCache)

// Capture the package directory before customer fixtures use t.Chdir.
var repositoryRoot, repositoryRootError = filepath.Abs("../..")

// DDTest uses the current CI run's binary, or builds it once per test process.
// TestMain's isolated temporary directory owns the locally built executable.
func DDTest(t testing.TB) string {
	t.Helper()
	if path := os.Getenv("DDTEST_TEST_BINARY"); path != "" {
		info, err := os.Stat(path)
		if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() {
			t.Fatalf("DDTEST_TEST_BINARY must name an existing absolute executable: %q", path)
		}
		return path
	}
	compiled.once.Do(func() { compiled.path, compiled.err = buildDDTest(t.Context()) })
	if compiled.err != nil {
		t.Fatal(compiled.err)
	}
	return compiled.path
}

func buildDDTest(ctx context.Context) (string, error) {
	if repositoryRootError != nil {
		return "", repositoryRootError
	}
	directory, err := os.MkdirTemp("", "ddtest-binary-")
	if err != nil {
		return "", err
	}
	name := "ddtest"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(directory, name)
	command := exec.CommandContext(ctx, "go", "build", "-o", path, ".")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		_ = os.RemoveAll(directory)
		return "", fmt.Errorf("build integration CLI: %w\n%s", err, output)
	}
	return path, nil
}
