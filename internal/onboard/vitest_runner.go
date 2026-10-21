// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"github.com/Masterminds/semver/v3"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// VitestRunnerCommand is the reproducible, local ddtest run command shape.
// Separate saved plans and CI-node assignments need their own validation.
type VitestRunnerCommand struct {
	Config  string
	Options []string
	Files   []string
}

func IsDDTestCommand(command string) bool { return filepath.Base(command) == "ddtest" }

func ParseVitestRunnerCommand(args []string) (VitestRunnerCommand, error) {
	var result VitestRunnerCommand
	run := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "run" && !run {
			run = true
			continue
		}
		if arg == "--" {
			result.Files = append(result.Files, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			if !run {
				return result, fmt.Errorf("expected ddtest run, got %q", arg)
			}
			result.Files = append(result.Files, arg)
			continue
		}
		key, value, inline := strings.Cut(arg, "=")
		switch key {
		case "--platform", "--framework", "--vitest-config", "--min-parallelism", "--max-parallelism", "--ci-job-overhead", "--target-time", "--tests-location", "--tests-exclude-pattern":
		default:
			return result, fmt.Errorf("ddtest run option %s requires separate validation; saved plans, CI-node mode and custom worker environments are not reproduced", key)
		}
		if !inline {
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "--") {
				return result, fmt.Errorf("missing value for %s", key)
			}
			value = args[i]
		}
		if key == "--platform" && value != "javascript" || key == "--framework" && value != "vitest" {
			return result, fmt.Errorf("this validation requires JavaScript and Vitest")
		}
		if key == "--vitest-config" {
			result.Config = value
		}
		if (key == "--vitest-config" || key == "--tests-location" || key == "--tests-exclude-pattern") && value != "" && !filepath.IsLocal(value) {
			return result, fmt.Errorf("%s must use a repository-relative path for isolated validation", key)
		}
		result.Options = append(result.Options, key+"="+value)
	}
	if !run {
		return result, fmt.Errorf("expected ddtest run")
	}
	for _, file := range result.Files {
		if !filepath.IsLocal(file) {
			return result, fmt.Errorf("test file selections must use repository-relative paths for isolated validation")
		}
	}
	return result, nil
}

// These are settings read by ddtest run itself. Other runner modes (saved plans,
// CI-node partitioning and worker overrides) cannot be reproduced by testdrive.
func VitestRunnerEnvironmentFlag(key string) (string, bool) {
	suffix, ok := strings.CutPrefix(key, "DD_TEST_OPTIMIZATION_RUNNER_")
	if !ok {
		return "", false
	}
	switch suffix {
	case "PLATFORM", "FRAMEWORK", "VITEST_CONFIG", "MIN_PARALLELISM", "MAX_PARALLELISM", "CI_JOB_OVERHEAD", "TARGET_TIME", "TESTS_LOCATION", "TESTS_EXCLUDE_PATTERN":
		return "--" + strings.ReplaceAll(strings.ToLower(suffix), "_", "-"), true
	}
	return "", false
}

// Turn inherited settings into explicit, recorded options; command flags win.
func VitestRunnerEnvironmentArguments(args []string, env map[string]string) ([]string, error) {
	result := slices.Clone(args)
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if !strings.HasPrefix(key, "DD_TEST_OPTIMIZATION_RUNNER_") || env[key] == "" {
			continue
		}
		flag, ok := VitestRunnerEnvironmentFlag(key)
		if !ok {
			return nil, fmt.Errorf("runner setting %s requires separate validation", key)
		}
		separator := slices.Index(result, "--")
		if separator < 0 {
			separator = len(result)
		}
		explicit := slices.ContainsFunc(result[:separator], func(arg string) bool { k, _, _ := strings.Cut(arg, "="); return k == flag })
		if !explicit {
			result = slices.Insert(result, separator, flag+"="+env[key])
		}
	}
	_, err := ParseVitestRunnerCommand(result)
	return result, err
}

// Recognize explicit pins only; installer scripts and floating aliases do not
// prove which binary a remote job ultimately executes.
func VitestRunnerCIPins(root string) []string {
	workflows, err := readWorkflows(root)
	if err != nil {
		return nil
	}
	var pins []string
	add := func(value string) {
		if version, err := semver.StrictNewVersion(strings.TrimPrefix(value, "v")); err == nil && version.Prerelease() == "" && !slices.Contains(pins, value) {
			pins = append(pins, value)
		}
	}
	for _, workflow := range workflows {
		add(workflow.Env["DDTEST_VERSION"])
		for _, job := range workflow.Jobs {
			add(job.Env["DDTEST_VERSION"])
			for _, step := range job.Steps {
				add(step.Env["DDTEST_VERSION"])
				for _, match := range ddtestReleasePin.FindAllStringSubmatch(step.Run, -1) {
					add(match[1])
				}
			}
		}
	}
	slices.Sort(pins)
	return pins
}

var ddtestReleasePin = regexp.MustCompile(`github\.com/(?:DataDog/ddtest|ddoghq/shepherd)/releases/download/(v?\d+\.\d+\.\d+)/`)
