// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
)

var pythonReleaseResolver = latestPythonRelease
var pythonInstalledReleaseResolver = installedPythonRelease

// The CLI prescribes one release per run, including any installed tracer that
// validation reuses. Tests replace both metadata sources to stay offline.
var pythonRelease struct {
	sync.Mutex
	value, installed string
	probed           bool
}

// SetPythonReleaseResolver replaces the latest-release lookup and clears the
// run's selection. The returned call restores the previous state.
func SetPythonReleaseResolver(resolve func(context.Context) (string, error)) func() {
	pythonRelease.Lock()
	defer pythonRelease.Unlock()
	previous, value, installed, probed := pythonReleaseResolver, pythonRelease.value, pythonRelease.installed, pythonRelease.probed
	pythonReleaseResolver, pythonRelease.value, pythonRelease.installed, pythonRelease.probed = resolve, "", "", false
	return func() {
		pythonRelease.Lock()
		defer pythonRelease.Unlock()
		pythonReleaseResolver, pythonRelease.value, pythonRelease.installed, pythonRelease.probed = previous, value, installed, probed
	}
}

// SetPythonInstalledReleaseResolver replaces the selected interpreter's tracer
// lookup. Tests use it without executing a local Python or uv process.
func SetPythonInstalledReleaseResolver(resolve func(context.Context) (string, error)) func() {
	pythonRelease.Lock()
	defer pythonRelease.Unlock()
	previous, value, installed, probed := pythonInstalledReleaseResolver, pythonRelease.value, pythonRelease.installed, pythonRelease.probed
	pythonInstalledReleaseResolver, pythonRelease.value, pythonRelease.installed, pythonRelease.probed = resolve, "", "", false
	return func() {
		pythonRelease.Lock()
		defer pythonRelease.Unlock()
		pythonInstalledReleaseResolver, pythonRelease.value, pythonRelease.installed, pythonRelease.probed = previous, value, installed, probed
	}
}

// SelectPythonRelease matches validation's installed-tracer precedence. Without
// an installed exact release, an exact selector wins; latest/empty resolves once
// through PyPI and falls back to the validated example when it is unavailable.
func SelectPythonRelease(requested string) (string, error) {
	if requested != "" && requested != "latest" && !exactPythonRelease.MatchString(requested) {
		return "", fmt.Errorf("the Python tracer version must be an exact ddtrace release, such as %s, or latest: %s", pythonExampleRelease, requested)
	}
	pythonRelease.Lock()
	defer pythonRelease.Unlock()
	if !pythonRelease.probed {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		installed, err := pythonInstalledReleaseResolver(ctx)
		cancel()
		if err == nil && exactPythonRelease.MatchString(installed) {
			pythonRelease.installed = installed
		}
		pythonRelease.probed = true
	}
	if pythonRelease.installed != "" {
		pythonRelease.value = pythonRelease.installed
	} else if requested != "" && requested != "latest" {
		pythonRelease.value = requested
	} else if pythonRelease.value == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		release, err := pythonReleaseResolver(ctx)
		cancel()
		if err != nil || !exactPythonRelease.MatchString(release) {
			release = pythonExampleRelease
		}
		pythonRelease.value = release
	}
	return pythonRelease.value, nil
}

func pythonSetupRelease() string {
	release, _ := SelectPythonRelease("")
	return release
}

func installedPythonRelease(ctx context.Context) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	selected := platform.NewPython()
	command, args := framework.NewPytest(selected).Command()
	command, args, _ = platform.PythonTestCommand(root, command, args, settings.GetCommand() != "")
	return selected.DetectTracer(ctx, platform.TracerOptions{Command: command, Args: args, Environment: map[string]string{
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED": "false", "DD_TRACE_ENABLED": "false", "DD_CIVISIBILITY_ENABLED": "false",
	}})
}

func latestPythonRelease(ctx context.Context) (string, error) {
	release, err := ResolvePythonRelease(ctx, "latest")
	return release.Version, err
}

func pythonReleaseNote(release string) string {
	return "The setup installs ddtrace " + release + ", the exact release ddtest selected, which local validation runs: write it as shown, never a floating or other release. An exact ddtrace already installed in the selected test environment takes precedence, as it does during validation. Otherwise ddtest uses --tracer-version or the latest PyPI release; rerun onboard with --tracer-version <release> to change that fallback."
}

func (pythonDiscovery) selectTracerRelease(requested string) (string, error) {
	return SelectPythonRelease(requested)
}
