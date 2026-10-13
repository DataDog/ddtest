// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestMochaTestdriveNYCPreviewAndExecution(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		args          []string
		installed     bool
		executable    bool
		inherited     bool
		jest          bool
		wrapped       bool
	}{
		{name: "local Mocha", command: "node_modules/.bin/mocha", installed: true, executable: true, wrapped: true},
		{name: "preserves configured command and arguments", command: "npx", args: []string{"mocha", "--config", "config with spaces.cjs", "--parallel"}, installed: true, executable: true, wrapped: true},
		{name: "NYC missing", command: "npx", args: []string{"mocha"}},
		{name: "NYC not executable", command: "npx", args: []string{"mocha"}, installed: true},
		{name: "already wrapped command", command: "node_modules/.bin/nyc", args: []string{"mocha"}, installed: true, executable: true},
		{name: "inherited wrapper", command: "npx", args: []string{"mocha"}, installed: true, executable: true, inherited: true},
		{name: "other framework", command: "npx", args: []string{"jest"}, installed: true, executable: true, jest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NYC_CONFIG", "")
			if tc.inherited {
				t.Setenv("NYC_CONFIG", `{"cwd":"project"}`)
			}
			drive := preparedTestdrive(t)
			drive.command, drive.args = tc.command, tc.args
			drive.framework = framework.NewMocha(platform.NewJavaScript())
			if tc.jest {
				drive.framework = framework.NewJest(platform.NewJavaScript())
			}
			if tc.installed {
				nyc := filepath.Join(drive.repositoryRoot, "node_modules", ".bin", "nyc")
				require.NoError(t, os.MkdirAll(filepath.Dir(nyc), 0755))
				require.NoError(t, os.WriteFile(nyc, []byte("#!/usr/bin/env node\n"), 0644))
				if tc.executable {
					require.NoError(t, os.Chmod(nyc, 0755))
				}
			}
			wantCommand, wantArgs := tc.command, tc.args
			if tc.wrapped {
				wantCommand = filepath.Join("node_modules", ".bin", "nyc")
				wantArgs = append([]string{"--", tc.command}, tc.args...)
			}
			var preview bytes.Buffer
			drive.Preview(&preview)
			wantDisplay := shellquote.Join(append([]string{wantCommand}, wantArgs...)...)
			require.Contains(t, preview.String(), "  - run: "+wantDisplay)
			_, err := os.Stat(drive.session.Directory())
			require.True(t, os.IsNotExist(err), "preview must remain read-only")
			installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
			drive.platform = installer
			executor := &fakeTestdriveExecutor{}
			drive.executor = executor
			drive.startIntake = func(string) (localIntake, error) {
				return &fakeIntake{url: "http://127.0.0.1:1234", findings: intake.Facts{TestEventCount: 1, TestCount: 1, CoveredTestCount: 1}}, nil
			}
			var output bytes.Buffer
			require.NoError(t, drive.Run(t.Context(), &output))
			require.Equal(t, wantCommand, executor.command)
			require.Equal(t, wantArgs, executor.args)
			require.Equal(t, tc.command, drive.command)
			require.Equal(t, tc.args, drive.args)
			data, err := os.ReadFile(filepath.Join(installer.sessionDirectory, diagnosticsFilename))
			require.NoError(t, err)
			var diagnostics runDiagnostics
			require.NoError(t, json.Unmarshal(data, &diagnostics))
			require.Equal(t, wantDisplay, diagnostics.Command)
			require.Contains(t, output.String(), "  Command: "+wantDisplay)
		})
	}
}
