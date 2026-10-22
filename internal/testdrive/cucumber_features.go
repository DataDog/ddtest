// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

func appendCucumberOptions(command string, args []string, options ...string) []string {
	if filepath.Base(command) == "npm" {
		return appendJestArgs(command, args, options...)
	}
	args = slices.Clone(args)
	if filepath.Base(command) != "cucumber-js" {
		for i, arg := range args {
			if arg == "npm" {
				return append(args[:i+1], appendJestArgs("npm", args[i+1:], options...)...)
			}
		}
	}
	runner := "cucumber-js"
	for _, arg := range args {
		if filepath.Base(arg) == "cucumber.js" {
			runner = "cucumber"
			break
		}
	}
	return framework.WithFrameworkOptions(command, args, runner, options...)
}

const cucumberProbeSteps = `const fs = require('node:fs');
const { Given, supportCodeLibraryBuilder: builder } = require('@cucumber/cucumber');
// Retain customer World, hooks and parameter types, but customer catch-all steps
// must not compete with the controlled definition. Finalize is exported by Cucumber.
const finalize = builder.finalize;
builder.finalize = function (...args) {
  const library = finalize.apply(this, args);
  library.stepDefinitions = library.stepDefinitions.filter(step => require('node:path').resolve(step.uri) === __filename);
  return library;
};
Given('the ddtest controlled scenario executes', function () {
  const counter = process.env.DDTEST_PROBE_COUNTER;
  fs.appendFileSync(counter, 'attempt\n');
  const attempts = fs.readFileSync(counter, 'utf8').trim().split('\n').length;
  if (process.env.DDTEST_PROBE_MODE === 'fail' ||
      (process.env.DDTEST_PROBE_MODE === 'fail-once' && attempts === 1)) {
    throw new Error('ddtest intentional failure');
  }
});
`

func (t *Testdrive) runCucumberFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	token := rand.Text()
	probe, err := createProbe(t.checkoutRoot(), baseline, "Feature: ddtest\n  Scenario: "+probeName+"\n    Given the ddtest controlled scenario executes "+token+"\n")
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.checkoutRoot(), probe)) }()
	steps := probe + ".steps.cjs"
	config := probe + ".config.cjs"
	if err := createOwnedProbe(t.checkoutRoot(), config, []byte("module.exports={default:{}};\n")); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.checkoutRoot(), config)) }()
	if err := createOwnedProbe(t.checkoutRoot(), steps, []byte(strings.ReplaceAll(cucumberProbeSteps, "the ddtest controlled scenario executes", "the ddtest controlled scenario executes "+token))); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.checkoutRoot(), steps)) }()

	child := *t
	child.cucumberSupport = baseline.cucumberSupport
	if baseline.cucumberParallel {
		leaf, err := onboard.CucumberProbeCommand(t.repositoryRoot, shellquote.Join(append([]string{t.command}, t.args...)...))
		if err != nil || !strings.Contains(leaf, "--parallel") {
			unavailableFeatures(result, "Cucumber parallel mode supplied through configuration cannot yet be replayed for isolated probes. Paired commands keep the original profile; select an explicit --parallel command for worker feature validation.")
			return nil
		}
	}
	err = runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		role := roleProbeFeature
		if scenario.Feature == "" {
			role = roleProbeControl
		}
		return child.runCucumber(ctx, output, session, preload, runSpec{Name: name, Role: role, Instrumented: true, Scenario: scenario, ProbePath: probe, ProbeMode: mode})
	}, result)
	if baseline.cucumberParallel {
		for i, feature := range result.Features {
			if feature.Status != "passed" {
				result.Features[i].Reason = "Cucumber parallel mode could not validate " + feature.Name + " with the selected tracer's worker control transport. " + feature.Reason
			}
		}
	}
	return err
}

func (t *Testdrive) cucumberProbeCommand(probe string) (string, []string, error) {
	leaf, err := onboard.CucumberProbeCommand(t.repositoryRoot, shellquote.Join(append([]string{t.command}, t.args...)...))
	if err != nil {
		return "", nil, err
	}
	words, err := shellquote.Split(leaf)
	if err != nil || len(words) == 0 {
		return "", nil, fmt.Errorf("invalid Cucumber probe command")
	}
	// Keep only the coverage wrapper. The empty owned config prevents additive
	// profile paths/tags/filters from reintroducing customer scenarios.
	runner := slices.IndexFunc(words, func(word string) bool { return filepath.Base(word) == "cucumber-js" })
	if runner < 0 {
		return "", nil, fmt.Errorf("cucumber probe runner was not resolved")
	}
	config, err := filepath.Rel(t.repositoryRoot, probe+".config.cjs")
	if err != nil {
		return "", nil, err
	}
	args := []string{"--config", config}
	for i := runner + 1; i < len(words); i++ {
		key, value, inline := strings.Cut(words[i], "=")
		if key == "--parallel" {
			if !inline {
				i++
				if i >= len(words) {
					return "", nil, fmt.Errorf("missing Cucumber parallel value")
				}
				value = words[i]
			}
			args = append(args, "--parallel", value)
		}
	}
	if t.cucumberSupport == nil {
		return "", nil, fmt.Errorf("cucumber effective support metadata is unavailable for feature probes")
	}
	for _, path := range t.cucumberSupport.RequirePaths {
		args = append(args, "--require", path)
	}
	for _, path := range t.cucumberSupport.ImportPaths {
		args = append(args, "--import", path)
	}
	for _, module := range t.cucumberSupport.RequireModules {
		args = append(args, "--require-module", module)
	}
	for _, loader := range t.cucumberSupport.Loaders {
		args = append(args, "--loader", loader)
	}
	args = append(args, probe, "--require", probe+".steps.cjs", "--retry", "0")
	if t.cucumberNode == "" || t.cucumberExecutable == "" {
		return "", nil, fmt.Errorf("cucumber preflight did not resolve its installed CLI")
	}
	native := append([]string{t.cucumberExecutable}, args...)
	if runner > 0 {
		wrapper := t.cucumberCoverage[filepath.Base(words[0])]
		if wrapper == "" {
			return "", nil, fmt.Errorf("cucumber coverage wrapper %s was not resolved in the selected package", words[0])
		}
		return t.cucumberNode, append(append([]string{wrapper}, words[1:runner]...), append([]string{t.cucumberNode}, native...)...), nil
	}
	return t.cucumberNode, native, nil
}
