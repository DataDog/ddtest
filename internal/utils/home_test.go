// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGetHomeDirFallsBackToSystemLookup(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("system lookup fallback test is for Unix-like hosts")
	}

	tempDir := t.TempDir()
	fakeHome := filepath.Join(tempDir, "home")
	t.Setenv("HOME", "")
	t.Setenv("PATH", tempDir)
	t.Setenv("DDTEST_FAKE_HOME", fakeHome)

	if runtime.GOOS == "darwin" {
		writeExecutable(t, filepath.Join(tempDir, "sh"), "#!/bin/sh\nprintf '%s\\n' \"$DDTEST_FAKE_HOME\"\n")
	} else {
		writeExecutable(t, filepath.Join(tempDir, "getent"), "#!/bin/sh\nprintf 'user:x:1:1::%s:/bin/sh\\n' \"$DDTEST_FAKE_HOME\"\n")
	}

	if got := getHomeDir(); got != fakeHome {
		t.Fatalf("getHomeDir() = %q, want %q", got, fakeHome)
	}
}

func TestGetHomeDirUsesHomeEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := getHomeDir(); got != home {
		t.Fatalf("getHomeDir() = %q, want %q", got, home)
	}
}

func TestGetHomeDirFallsBackToShell(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "plan9" {
		t.Skip("shell fallback test is for non-darwin Unix-like hosts")
	}

	tempDir := t.TempDir()
	fakeHome := filepath.Join(tempDir, "shell-home")
	t.Setenv("HOME", "")
	t.Setenv("PATH", tempDir)
	t.Setenv("DDTEST_FAKE_HOME", fakeHome)

	writeExecutable(t, filepath.Join(tempDir, "getent"), "#!/bin/sh\nexit 1\n")
	writeExecutable(t, filepath.Join(tempDir, "sh"), "#!/bin/sh\nprintf '%s\\n' \"$DDTEST_FAKE_HOME\"\n")

	if got := getHomeDir(); got != fakeHome {
		t.Fatalf("getHomeDir() = %q, want %q", got, fakeHome)
	}
}

func TestGetHomeDirReturnsEmptyWhenFallbacksFail(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "plan9" {
		t.Skip("fallback failure test is for non-darwin Unix-like hosts")
	}

	tempDir := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("PATH", tempDir)

	writeExecutable(t, filepath.Join(tempDir, "getent"), "#!/bin/sh\nexit 1\n")
	writeExecutable(t, filepath.Join(tempDir, "sh"), "#!/bin/sh\nexit 1\n")

	if got := getHomeDir(); got != "" {
		t.Fatalf("getHomeDir() = %q, want empty string", got)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
