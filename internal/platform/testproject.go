// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package platform

import (
	"fmt"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

// DetectTestProject performs read-only detection for onboarding and testdrive.
// A framework hint resolves repositories containing more than one test runner.
func DetectTestProject(root, hint string) (string, framework.Framework, error) {
	candidates := []struct {
		platform   Platform
		frameworks []framework.Framework
	}{
		{NewJavaScript(), []framework.Framework{framework.NewJest(), framework.NewMocha(), framework.NewCypress(), framework.NewPlaywright(), framework.NewCucumber(), framework.NewVitest()}},
		{NewPython(), []framework.Framework{framework.NewPytest()}},
		{NewRuby(settings.TestSkippingLevelTest), []framework.Framework{framework.NewRSpec(), framework.NewMinitest()}},
	}
	var names []string
	var selected framework.Framework
	var language string
	for _, candidate := range candidates {
		found, err := candidate.platform.Detect(root)
		if err != nil {
			return "", nil, err
		}
		if !found {
			continue
		}
		for _, runner := range candidate.frameworks {
			if hint != "" && runner.Name() != hint {
				continue
			}
			found, err := runner.Detect(root)
			if err != nil {
				return "", nil, err
			}
			if found {
				names = append(names, runner.Name())
				selected = runner
				language = candidate.platform.Name()
			}
		}
	}
	if len(names) == 0 {
		return "", nil, fmt.Errorf("could not detect a supported test framework %q; inspect package.json, Python test configuration, or Gemfile", hint)
	}
	if len(names) > 1 {
		return "", nil, fmt.Errorf("found multiple test frameworks (%s); select one with --framework", strings.Join(names, ", "))
	}
	return language, selected, nil
}
