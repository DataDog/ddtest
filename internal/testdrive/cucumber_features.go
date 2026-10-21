// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	if i := framework.FrameworkSeparator(command, args, "cucumber-js"); i >= 0 {
		return slices.Insert(args, i, options...)
	}
	return append(args, options...)
}

const cucumberProbeSteps = `const fs = require('node:fs');
const { Given } = require('@cucumber/cucumber');
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
	probe, err := createProbe(t.repositoryRoot, baseline, "Feature: ddtest\n  Scenario: "+probeName+"\n    Given the ddtest controlled scenario executes\n")
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.repositoryRoot, probe)) }()
	steps := probe + ".steps.cjs"
	file, err := os.OpenFile(steps, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(cucumberProbeSteps)
	err = errors.Join(writeErr, file.Close())
	defer func() { runErr = errors.Join(runErr, os.Remove(steps)) }()
	if err != nil {
		return err
	}
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		return t.runCucumber(ctx, output, session, preload, name, true, scenario, probe, mode)
	}, result)
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
	var args []string
	// Positional paths select the disposable feature only. Keep configuration,
	// profiles, tags, support imports, parallelism and all other options intact.
	values := []string{"--config", "-c", "--format", "-f", "--format-options", "--import", "-i", "--language", "--loader", "-l", "--name", "--order", "--parallel", "--profile", "-p", "--require", "-r", "--require-module", "--retry-tag-filter", "--snippet-interface", "--snippet-syntax", "--tags", "-t", "--world-parameters"}
	for i := 1; i < len(words); i++ {
		word := words[i]
		if word == "--retry" {
			if i+1 >= len(words) {
				return "", nil, fmt.Errorf("missing Cucumber retry value")
			}
			i++
			continue
		}
		if strings.HasPrefix(word, "--retry=") {
			continue
		}
		if slices.Contains(values, word) {
			if i+1 >= len(words) {
				return "", nil, fmt.Errorf("missing Cucumber option value: %s", word)
			}
			args = append(args, word, words[i+1])
			i++
			continue
		}
		if strings.HasPrefix(word, "-") {
			args = append(args, word)
		}
	}
	args = append(args, probe, "--import", probe+".steps.cjs", "--retry", "0")
	command := filepath.Join(t.repositoryRoot, "node_modules/.bin/cucumber-js")
	return command, args, nil
}
