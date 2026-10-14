// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
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
			require.Contains(t, preview.String(), "  - command: "+wantDisplay)
			_, err := os.Stat(drive.session.Directory())
			require.True(t, os.IsNotExist(err), "preview must remain read-only")
			if tc.jest {
				command, args := drive.executionCommand()
				require.Equal(t, wantCommand, command)
				require.Equal(t, wantArgs, args)
				return
			}
			command, args := drive.executionCommand()
			require.Equal(t, wantCommand, command)
			require.Equal(t, wantArgs, args)
			require.Equal(t, tc.command, drive.command)
			require.Equal(t, tc.args, drive.args)
		})
	}
}

func TestMochaDoesNotDoubleWrapCoveredPackageScripts(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.framework = framework.NewMocha(platform.NewJavaScript())
	drive.command, drive.args = "npm", []string{"test"}
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, "package.json"), `{"scripts":{"test":"npx --no-install nyc -- c8 mocha test.js"}}`)
	bin := filepath.Join(drive.repositoryRoot, "node_modules/.bin")
	require.NoError(t, os.MkdirAll(bin, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nyc"), []byte("#!/bin/sh\n"), 0700))
	command, args := drive.executionCommand()
	require.Equal(t, "npm", command)
	require.Equal(t, []string{"test"}, args)
}
