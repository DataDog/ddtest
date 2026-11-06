// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// A go-run executable disappears after the command exits. It cannot be used in
// a protocol handoff; require a persistent build unless one is already on PATH.
const TransientExecutableReason = "The temporary go run executable cannot be reused. Run make build in the ddtest source checkout, then invoke that persistent ddtest binary to continue."

func persistentExecutable(path string) bool {
	if filepath.Base(filepath.Dir(path)) != "exe" {
		return true
	}
	for directory := filepath.Dir(path); filepath.Dir(directory) != directory; directory = filepath.Dir(directory) {
		if strings.HasPrefix(filepath.Base(directory), "go-build") {
			return false
		}
	}
	return true
}

func DDTestCommand(args ...string) []string {
	executable, _ := ddtestExecutable()
	if executable == "" {
		return nil
	}
	command := append([]string{executable}, args...)
	// Follow-up commands keep the selected CI provider.
	if provider := ActiveCIProvider(); provider != "" && len(args) > 0 && (args[0] == "onboard" || args[0] == "testdrive") && !slices.Contains(args, "--ci-provider") {
		command = append(command, "--ci-provider", provider)
	}
	// And the user's statements about CI, which reports are bound to.
	if len(args) > 0 && (args[0] == "onboard" || args[0] == "testdrive") && !slices.Contains(args, "--ci-runtime") && !slices.Contains(args, "--ci-confirm") {
		command = append(command, CIStatementArguments()...)
	}
	return command
}

// SelfPathVariable names an executable that returned commands use in place of
// the running one, such as a wrapper that runs this ddtest.
const SelfPathVariable = "DDTEST_SELF_PATH"

// selfPath is SelfPathVariable's value when it is an absolute path to an
// executable regular file; empty otherwise.
func selfPath() string {
	path := os.Getenv(SelfPathVariable)
	if !filepath.IsAbs(path) {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return ""
	}
	return path
}

// ddtestExecutable prefers SelfPathVariable, then the running executable. Only
// a temporary go run binary falls back to ddtest on PATH, which may be a
// different version.
func ddtestExecutable() (path string, fromPATH bool) {
	if path := selfPath(); path != "" {
		return path, false
	}
	executable, err := os.Executable()
	if err == nil && persistentExecutable(executable) {
		return executable, false
	}
	executable, err = exec.LookPath("ddtest")
	if err != nil || !persistentExecutable(executable) {
		return "", false
	}
	return executable, true
}

// ExecutableFallbackNotice says when handoff commands use ddtest from PATH
// instead of the running executable; empty otherwise.
func ExecutableFallbackNotice() string {
	if executable, fromPATH := ddtestExecutable(); fromPATH {
		return "This executable is temporary (go run), so commands use ddtest from PATH (" + executable + "), which may be a different version."
	}
	return ""
}

func OnboardingCommand(frameworks ...string) []string {
	framework := "vitest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	return DDTestCommand("onboard", "--framework", framework, "--apply", "--yes")
}
