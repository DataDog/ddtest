package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func packageRepository(t *testing.T) string {
	t.Helper()
	root := onboard.CanonicalDirectory(t.TempDir())
	for name, contents := range map[string]string{
		"test/package.json":       `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11"}}`,
		".buildkite/pipeline.yml": "steps:\n  - command: cd test && npm test\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
	output, err := exec.Command("git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(output))
	return root
}

func locationCommand(t *testing.T, name string, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cfg := settings.Get()
	old := *cfg
	cfg.Platform, cfg.Framework, cfg.Command = "", "", ""
	t.Cleanup(func() {
		*cfg = old
		platform.UsePackageDirectory("")
		platform.UseDetectionHint("")
		testdrive.UseReportRoot("", "")
	})
	command := &cobra.Command{Use: name}
	command.Flags().Bool("all", false, "")
	command.Flags().Bool("report", false, "")
	require.NoError(t, command.Flags().Parse(args))
	var stderr bytes.Buffer
	command.SetErr(&stderr)
	return command, &stderr
}

func workingDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	require.NoError(t, err)
	return onboard.CanonicalDirectory(directory)
}

func TestOnboardFromAPackageRunsAtTheCIRoot(t *testing.T) {
	root := packageRepository(t)
	for _, tc := range []struct {
		name string
		args []string
	}{{"onboard", nil}, {"testdrive", []string{"--all"}}, {"testdrive", []string{"--report"}}} {
		t.Run(tc.name+" "+filepath.Join(tc.args...), func(t *testing.T) {
			t.Chdir(filepath.Join(root, "test"))
			command, stderr := locationCommand(t, tc.name, tc.args...)
			require.NoError(t, locatePackage(command))
			require.Equal(t, root, workingDirectory(t))
			require.Equal(t, "test", platform.PackageDirectory())
			require.Contains(t, stderr.String(), "onboarding runs there for the JavaScript package in test/")
		})
	}
}

func TestFocusedTestdriveFromAPackageStaysInThePackage(t *testing.T) {
	root := packageRepository(t)
	t.Chdir(filepath.Join(root, "test"))
	command, stderr := locationCommand(t, "testdrive")
	require.NoError(t, locatePackage(command))
	require.Equal(t, filepath.Join(root, "test"), workingDirectory(t))
	require.Equal(t, ".", platform.PackageDirectory())
	require.Contains(t, stderr.String(), "this run executes in test/ and keeps its reports in "+root+"/.testoptimization")
}

func TestOnboardAtARootWithoutManifestSelectsTheCIPackage(t *testing.T) {
	root := packageRepository(t)
	t.Chdir(root)
	command, stderr := locationCommand(t, "onboard")
	require.NoError(t, locatePackage(command))
	require.Equal(t, root, workingDirectory(t))
	require.Equal(t, "test", platform.PackageDirectory())
	require.Contains(t, stderr.String(), "using the JavaScript package in test/")
	detected, err := platform.DetectPlatform()
	require.NoError(t, err)
	require.Equal(t, "javascript", detected.Name())

	require.NoError(t, os.WriteFile(filepath.Join(root, ".buildkite", "pipeline.yml"), []byte("steps:\n  - command: make\n"), 0o644))
	command, _ = locationCommand(t, "onboard")
	require.NoError(t, locatePackage(command))
	require.Equal(t, ".", platform.PackageDirectory())
	_, err = platform.DetectPlatform()
	require.ErrorContains(t, err, "JavaScript packages below it: test. Run ddtest onboard in the package directory CI tests (for example cd test && ddtest onboard)")
}
