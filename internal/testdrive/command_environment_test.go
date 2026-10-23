package testdrive

import (
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitCommandExtractsLiteralEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"jest":"29"}}`)
	viper.Reset()
	viper.Set("framework", "jest")
	viper.Set("command", `OUTPUT='two words' npx jest --ci`)
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	drive, err := Prepare(PrepareOptions{TracerVersion: "6.20.1", CheckOnly: true})
	require.NoError(t, err)
	require.NotContains(t, drive.command, "OUTPUT=")
	require.Equal(t, "two words", drive.configurationEnvironment["OUTPUT"])
	require.Equal(t, "two words", drive.preflightEnvironment["OUTPUT"])
}

func TestJestCIOptionsReachTheRunWithoutBlockingDiscovery(t *testing.T) {
	root := t.TempDir()
	writeJestManifest(t, root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --experimental-vm-modules'}\n")
	t.Chdir(root)
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	drive, err := Prepare(PrepareOptions{TracerVersion: "latest"})
	require.NoError(t, err)
	require.NoError(t, drive.discoveryError)
	require.Equal(t, "--experimental-vm-modules", drive.nodeOptions(), "CI's own options apply without the Datadog preload")
}
