// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPrepareUsesCICommandWithoutLoadingJestConfig(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	previous := settings.Get().Command
	settings.Get().Command = ""
	t.Cleanup(func() { settings.Get().Command = previous })
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest","test:ci":"jest --runInBand --no-cache --coverage --verbose"},"devDependencies":{"jest":"29.7.0"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: npm run test:ci\n")
	requireWriteFile(t, filepath.Join(root, "jest.config.js"), "throw new Error('prepare must not execute config')")
	drive, err := Prepare("latest")
	require.NoError(t, err)
	require.Equal(t, "npm", drive.command)
	require.Equal(t, []string{"run", "test:ci"}, drive.args)
	var output bytes.Buffer
	drive.Preview(&output)
	require.Contains(t, output.String(), "run: npm run test:ci")
	settings.Get().Command = "node_modules/.bin/jest --config explicit.js"
	drive, err = Prepare("latest")
	require.NoError(t, err)
	require.Equal(t, "node_modules/.bin/jest", drive.command)
	require.Equal(t, []string{"--config", "explicit.js"}, drive.args)
}

func TestJestCoverageOutputUsesSessionDirectory(t *testing.T) {
	drive := preparedTestdrive(t)
	executor := &fakeTestdriveExecutor{}
	drive.executor = executor
	drive.startIntake = func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil }
	session, err := NewSession()
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()
	for _, probe := range []string{"", "probe.test.js"} {
		_, err := drive.runJest(t.Context(), &bytes.Buffer{}, session, "/tracer/ci/init.js", "baseline", false, intake.Scenario{}, probe, "pass")
		require.NoError(t, err)
		index := slices.Index(executor.args, "--coverageDirectory")
		require.NotEqual(t, -1, index)
		require.Equal(t, filepath.Join(session.Directory(), "baseline", "coverage"), executor.args[index+1])
	}
}

func TestJestValidationPreservesCommandSeparators(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		args, prefix  []string
	}{
		{"direct", "jest", []string{"--runInBand", "--", "original.test.js"}, []string{"--runInBand"}},
		{"node", "node", []string{"node_modules/jest/bin/jest.js", "--", "original.test.js"}, []string{"node_modules/jest/bin/jest.js"}},
		{"npx wrapper", "npx", []string{"--", "jest", "--", "original.test.js"}, []string{"--", "jest"}},
		{"npm script", "npm", []string{"test", "--", "--", "original.test.js"}, []string{"test", "--"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.args)
			want := append(slices.Clone(tc.prefix), "--json", "--", "original.test.js")
			require.Equal(t, want, appendJestArgs(tc.command, tc.args, "--json"))
			run := preparedTestdrive(t)
			run.command, run.args = tc.command, tc.args
			executor := &fakeTestdriveExecutor{output: []byte(`["probe.test.js"]`)}
			run.executor = executor
			files, _, err := run.discoverJestTests(t.Context(), "probe.test.js")
			require.NoError(t, err)
			require.Equal(t, []string{"probe.test.js"}, files)
			want = append(slices.Clone(tc.prefix), "--listTests", "--json", "--runTestsByPath", "probe.test.js", "--")
			require.Equal(t, want, executor.args)
			run.startIntake = func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil }
			session, err := NewSession()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, session.Close()) })
			_, err = run.runJest(t.Context(), &bytes.Buffer{}, session, "/tracer/ci/init.js", "probe", true, intake.Scenario{}, "probe.test.js", "pass")
			require.NoError(t, err)
			require.NotContains(t, executor.args, "original.test.js")
			require.Equal(t, "--", executor.args[len(executor.args)-1], "generated options must precede Jest's separator")
			require.Contains(t, executor.args, "--coverageThreshold={}")
			require.Contains(t, executor.args, "--testNamePattern")
			require.Equal(t, original, tc.args, "probe commands must not mutate the compatibility command")
			selected, ok := projectArgs(tc.command, tc.args, "Web")
			require.True(t, ok)
			want = append(slices.Clone(tc.prefix), "--selectProjects", "Web", "--", "original.test.js")
			require.Equal(t, want, selected)
		})
	}
}

func TestJestValidationPreservesPackageManagerForwarding(t *testing.T) {
	require.Equal(t, []string{"test", "--", "--json"}, appendJestArgs("npm", []string{"test"}, "--json"))
	require.Equal(t, []string{"test", "--", "--runInBand", "--json"}, appendJestArgs("npm", []string{"test", "--", "--runInBand"}, "--json"))
	require.Equal(t, []string{"--", "jest", "--json"}, appendJestArgs("npx", []string{"--", "jest"}, "--json"))
}
