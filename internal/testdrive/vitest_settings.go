// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
)

func (t *Testdrive) resolveVitestSettings() error {
	if t.usesVitestRunner() {
		env := map[string]string{}
		for _, entry := range os.Environ() {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
		}
		maps.Copy(env, t.configurationEnvironment)
		if settings.GetVitestConfig() != "" {
			env["DD_TEST_OPTIMIZATION_RUNNER_VITEST_CONFIG"] = settings.GetVitestConfig()
		}
		args, err := onboard.VitestRunnerEnvironmentArguments(t.args, env)
		if err != nil {
			return err
		}
		t.args = args
	} else if config := settings.GetVitestConfig(); config != "" {
		separator := slices.Index(t.args, "--")
		if separator < 0 {
			separator = len(t.args)
		}
		if !slices.ContainsFunc(t.args[:separator], func(arg string) bool {
			key, _, _ := strings.Cut(arg, "=")
			return key == "--config" || key == "-c" || strings.HasPrefix(key, "-c=")
		}) {
			t.args = framework.WithFrameworkOptions(t.command, t.args, "vitest", "--config", config)
		}
	}
	return nil
}
