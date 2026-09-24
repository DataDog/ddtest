package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestTestdriveUsesFrameworkCommand(t *testing.T) {
	old := settings.Get().Command
	settings.Get().Command = ""
	t.Cleanup(func() { settings.Get().Command = old })
	for _, tc := range []struct {
		runner framework.Framework
		args   []string
	}{
		{framework.NewJest(), []string{"jest"}},
		{framework.NewMocha(), []string{"mocha"}},
		{framework.NewVitest(), []string{"vitest", "run"}},
		{framework.NewPlaywright(), []string{"playwright", "test"}},
		{framework.NewCucumber(), []string{"cucumber-js"}},
	} {
		t.Run(tc.runner.Name(), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			// Neither a custom script nor a package manager lockfile overrides execution.
			require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"jest && echo side-effect","unit":"vitest --config custom.ts"}}`), 0644))
			require.NoError(t, os.WriteFile("yarn.lock", nil, 0644))
			command, args := tc.runner.Command()
			require.Equal(t, "npx", command)
			require.Equal(t, tc.args, args)
		})
	}
}

func TestTestdrivePreservesExplicitCommandArguments(t *testing.T) {
	t.Cleanup(func() { settings.Get().Command = "" })
	settings.Get().Command = `npm run smoke -- --config "config with spaces.js"`
	command, args := framework.NewMocha().Command()
	require.Equal(t, "npm", command)
	require.Equal(t, []string{"run", "smoke", "--", "--config", "config with spaces.js"}, args)
}

func TestTestdriveMinitestRequiresExecutableRailsBinstub(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "rails"), []byte("#!/bin/sh\n"), 0644))
	command, args, err := TestdriveCommand(root, NewMinitest())
	require.NoError(t, err)
	require.Equal(t, "bundle", command)
	require.Equal(t, []string{"exec", "rake", "test"}, args)
}
