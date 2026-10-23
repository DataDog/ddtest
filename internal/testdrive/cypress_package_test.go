package testdrive

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
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
	requireWriteFile(t, filepath.Join(module, "bin/cypress"), `require('node:fs').writeFileSync('inventory-called','yes');console.log('Detected 1 browser installed:\n1. Firefox\n - Name: firefox\n - Channel: stable\n')`)
	tracer := filepath.Join(directory, "node_modules/dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci"), 0700))
	requireWriteFile(t, filepath.Join(tracer, "ci/init.js"), `module.exports={}`)
	requireWriteFile(t, filepath.Join(tracer, "package.json"), `{"version":"6.20.1","engines":{"node":">=22"}}`)
	t.Setenv("DD_TRACE_PACKAGE", "")
	t.Chdir(directory)
	drive := Testdrive{platform: platform.NewJavaScript(), tracerLabel: "dd-trace@latest", repositoryRoot: directory, configurationRoot: root, executor: cypressPackageExecutor{directory}, nodeVersion: func() string { return "24.14.1" }, configurationEnvironment: map[string]string{"NODE_OPTIONS": ""}, cypress: &onboard.CypressCommand{Options: map[string]any{"browser": "chrome"}}}
	result := validationResult{Tracer: drive.tracerLabel}
	// The inventory comes from frontend's package, and a missing browser still
	// reports the project tracer rather than the requested label.
	require.ErrorContains(t, drive.checkCypressPreflight(t.Context(), io.Discard, &result), "cypress browser preflight")
	require.Equal(t, "15.1.0", result.Preflight.Version)
	require.FileExists(t, filepath.Join(directory, "inventory-called"))
	require.NoDirExists(t, filepath.Join(root, "node_modules"))
	require.Equal(t, "dd-trace@6.20.1 · project", tracerDescription(result))
}

func TestCypressRootAliasInspectsItsConsumerPackage(t *testing.T) {
	t.Setenv("DD_TRACE_PACKAGE", "")
	t.Setenv("NODE_OPTIONS", "")
	root := t.TempDir()
	directory := filepath.Join(root, "frontend")
	module := filepath.Join(directory, "node_modules/cypress")
	require.NoError(t, os.MkdirAll(filepath.Join(module, "bin"), 0700))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"npm --prefix frontend test"}}`)
	requireWriteFile(t, filepath.Join(directory, "package.json"), `{"scripts":{"test":"cypress run"}}`)
	requireWriteFile(t, filepath.Join(module, "package.json"), `{"version":"15.1.0","engines":{"node":">=20"}}`)
	requireWriteFile(t, filepath.Join(module, "bin/cypress"), `console.log('Detected 1 browser installed:\n1. Chrome\n - Name: chrome\n - Channel: stable\n')`)
	tracer := filepath.Join(directory, "node_modules/dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci"), 0700))
	requireWriteFile(t, filepath.Join(tracer, "ci/init.js"), `module.exports={}`)
	requireWriteFile(t, filepath.Join(tracer, "package.json"), `{"version":"6.20.1","engines":{"node":">=22"}}`)
	entry, err := onboard.CypressExecutionCommand(root, "npm test")
	require.NoError(t, err)
	t.Chdir(root)
	drive := Testdrive{platform: platform.NewJavaScript(), repositoryRoot: root, cypressPackage: entry.CypressPackage, executor: cypressPackageExecutor{root}, nodeVersion: func() string { return "24.14.1" }, cypress: &onboard.CypressCommand{Options: map[string]any{"browser": "chrome"}}}
	var result validationResult
	require.NoError(t, drive.checkCypressPreflight(t.Context(), io.Discard, &result))
	expected, err := filepath.EvalSymlinks(filepath.Join(tracer, "ci/init.js"))
	require.NoError(t, err)
	require.Equal(t, expected, drive.projectTracer)
	require.Equal(t, "6.20.1", result.Selection.Version)
	require.Equal(t, "project", result.Selection.Source)
	// A different root installation must not replace the consumer's tracer.
	other := filepath.Join(root, "node_modules/dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(other, "ci"), 0700))
	requireWriteFile(t, filepath.Join(other, "ci/init.js"), `module.exports={}`)
	requireWriteFile(t, filepath.Join(other, "package.json"), `{"version":"6.21.0","engines":{"node":">=22"}}`)
	drive.projectTracer = filepath.Join(other, "ci/init.js")
	drive.configurationEnvironment = map[string]string{}
	require.NoError(t, drive.checkCypressPreflight(t.Context(), io.Discard, &result))
	require.Equal(t, "6.20.1", result.Selection.Version)
	require.Equal(t, expected, drive.projectTracer)
	require.NoError(t, drive.verifyInstalledSelection(t.Context(), drive.projectTracer, &result))
	require.Equal(t, "15.1.0", result.Preflight.Version)
}
