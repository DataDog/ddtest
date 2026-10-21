package testdrive

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

type cypressPackageExecutor struct{ directory string }

func (e cypressPackageExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = e.directory
	command.Env = os.Environ()
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	return command.CombinedOutput()
}

func TestCypressBrowserInventoryUsesSelectedPackage(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is needed for native module resolution")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "frontend")
	module := filepath.Join(directory, "node_modules/cypress")
	require.NoError(t, os.MkdirAll(filepath.Join(module, "bin"), 0700))
	requireWriteFile(t, filepath.Join(directory, "package.json"), `{}`)
	requireWriteFile(t, filepath.Join(module, "package.json"), `{"version":"15.1.0","engines":{"node":">=20"}}`)
	requireWriteFile(t, filepath.Join(module, "bin/cypress"), `require('node:fs').writeFileSync('inventory-called','yes');console.log('Detected 1 browser installed:\n1. Chrome\n - Name: chrome\n - Channel: stable\n')`)
	drive := Testdrive{repositoryRoot: directory, configurationRoot: root, executor: cypressPackageExecutor{directory}, nodeVersion: func() string { return "24.14.1" }, configurationEnvironment: map[string]string{"NODE_OPTIONS": ""}, cypress: &onboard.CypressCommand{Options: map[string]any{"browser": "chrome"}}}
	var result validationResult
	// This harness deliberately has no tracer platform; inventory must complete
	// from frontend's package before preflight reaches tracer selection.
	require.Error(t, drive.checkCypressPreflight(t.Context(), io.Discard, &result))
	require.Equal(t, "15.1.0", result.Preflight.Version)
	require.FileExists(t, filepath.Join(directory, "inventory-called"))
	require.NoDirExists(t, filepath.Join(root, "node_modules"))
}
