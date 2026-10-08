package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestTestdriveUsesFrameworkCommand(t *testing.T) {
	old := settings.Get().Command
	settings.Get().Command = ""
	t.Cleanup(func() { settings.Get().Command = old })
	javascript := platform.NewJavaScript()
	for _, tc := range []struct {
		runner  framework.Framework
		command string
		args    []string
	}{
		{framework.NewJest(javascript), "npx", []string{"jest"}},
		{framework.NewMocha(javascript), "npx", []string{"mocha"}},
		{framework.NewVitest(javascript), "node", nil},
		{framework.NewPlaywright(javascript), "npx", []string{"playwright", "test"}},
		{framework.NewCucumber(javascript), "npx", []string{"cucumber-js"}},
	} {
		t.Run(tc.runner.Name(), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			// Neither a custom script nor a package manager lockfile overrides execution.
			require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"jest && echo side-effect","unit":"vitest --config custom.ts"}}`), 0644))
			require.NoError(t, os.WriteFile("yarn.lock", nil, 0644))
			command, args := tc.runner.Command()
			require.Equal(t, tc.command, command)
			require.Equal(t, tc.args, args)
		})
	}
}

func TestTestdrivePreservesExplicitCommandArguments(t *testing.T) {
	t.Cleanup(func() { settings.Get().Command = "" })
	settings.Get().Command = `npm run smoke -- --config "config with spaces.js"`
	command, args := framework.NewMocha(platform.NewJavaScript()).Command()
	require.Equal(t, "npm", command)
	require.Equal(t, []string{"run", "smoke", "--", "--config", "config with spaces.js"}, args)
}

func TestTestdriveMinitestRequiresExecutableRailsBinstub(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "rails"), []byte("#!/bin/sh\n"), 0644))
	command, args := framework.NewMinitest().Command()
	require.Equal(t, "bundle", command)
	require.Equal(t, []string{"exec", "rake", "test"}, args)
}
