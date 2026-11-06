// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// pythonReleaseResolver reads the latest exact ddtrace release. Tests
// replace it (see SetPythonReleaseResolver), so they never reach the network.
var pythonReleaseResolver = latestPythonRelease

// pythonRelease is the ddtrace release this run prescribes, once
// selected or resolved.
var pythonRelease struct {
	sync.Mutex
	value string
}

// SetPythonReleaseResolver replaces how ddtest reads the latest ddtrace
// release, forgetting any release already selected, and returns the call
// that restores the previous resolver. Tests use it to stay offline.
func SetPythonReleaseResolver(resolve func(context.Context) (string, error)) (restore func()) {
	pythonRelease.Lock()
	defer pythonRelease.Unlock()
	previous, selected := pythonReleaseResolver, pythonRelease.value
	pythonReleaseResolver, pythonRelease.value = resolve, ""
	return func() {
		pythonRelease.Lock()
		defer pythonRelease.Unlock()
		pythonReleaseResolver, pythonRelease.value = previous, selected
	}
}

// SelectPythonRelease selects the ddtrace release every Python setup
// instruction, snippet and message of this run names, which validation then
// runs as the release CI pins: requested (--tracer-version) when it is an
// exact release, otherwise, for latest or empty, the latest release on PyPI
// (see pythonSetupRelease). Any other selector is an error: a floating
// release cannot be validated.
func SelectPythonRelease(requested string) (string, error) {
	if requested != "" && requested != "latest" {
		if !exactPythonRelease.MatchString(requested) {
			return "", fmt.Errorf("the Python tracer version must be an exact ddtrace release, such as %s, or latest: %s", pythonExampleRelease, requested)
		}
		pythonRelease.Lock()
		defer pythonRelease.Unlock()
		pythonRelease.value = requested
		return requested, nil
	}
	return pythonSetupRelease(), nil
}

// pythonSetupRelease is the release SelectPythonRelease selected, or else the
// latest release on PyPI, read once per run where a Python setup is
// first written, as the other languages resolve their tracer when they need
// it. When PyPI can't be read, it is pythonExampleRelease, an
// exact release ddtest has validated: the instructions still name one exact
// release, which validation then downloads.
func pythonSetupRelease() string {
	pythonRelease.Lock()
	defer pythonRelease.Unlock()
	if pythonRelease.value == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		release, err := pythonReleaseResolver(ctx)
		if err != nil || !exactPythonRelease.MatchString(release) {
			release = pythonExampleRelease
		}
		pythonRelease.value = release
	}
	return pythonRelease.value
}

// latestPythonRelease uses the same PyPI metadata lookup as validation.
func latestPythonRelease(ctx context.Context) (string, error) {
	release, err := ResolvePythonRelease(ctx, "latest")
	return release.Version, err
}

// pythonReleaseNote says, in a setup's notes, which ddtrace release the
// setup installs and why it is exact.
func pythonReleaseNote(release string) string {
	return "The setup installs ddtrace " + release + ", the exact release ddtest selected (the latest on PyPI, or --tracer-version), which local validation runs: write it as shown, never a floating or other release. To set up another exact release, rerun onboard with --tracer-version <release>."
}

func (pythonDiscovery) selectTracerRelease(requested string) (string, error) {
	return SelectPythonRelease(requested)
}
