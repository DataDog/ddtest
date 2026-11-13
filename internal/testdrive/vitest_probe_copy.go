// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
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

	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// Explicit file allowlists cannot admit a new sibling probe. Append the probe
// to an executed file in a disposable checkout instead, preserving its imports
// and the original configuration. Never replace a customer's test file.
func (t *Testdrive) runVitestCopiedFeatures(ctx context.Context, output io.Writer, session *workspace.Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	parent, err := os.MkdirTemp("", "ddtest-vitest-probe-")
	if err != nil {
		return err
	}
	if err := workspace.MarkStorage(parent, t.ownershipRoot()); err != nil {
		return errors.Join(err, os.Remove(parent))
	}
	defer func() { runErr = errors.Join(runErr, workspace.RemoveMarkedStorage(parent)) }()
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	parent = canonicalParent
	source, target := workspace.CanonicalRoot(t.checkoutRoot()), filepath.Join(parent, "checkout")
	size, err := configurationCopyEstimate(ctx, source, nil)
	if err == nil {
		err = checkConfigurationSpace(parent, size)
	}
	if err == nil {
		_, _ = fmt.Fprintf(output, "The configuration excludes new probe files; isolating an existing test file for feature controls (up to %d bytes).\n", size)
		err = workspace.Copy(ctx, source, target, nil)
	}
	if err != nil {
		unavailableFeatures(result, "Could not isolate an existing Vitest probe file: "+err.Error())
		return nil
	}
	rebase := func(path string) string {
		if filepath.IsAbs(path) {
			canonical := path
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				canonical = resolved
			}
			if relative, err := filepath.Rel(source, canonical); err == nil && (relative == "." || filepath.IsLocal(relative)) {
				return filepath.Join(target, relative)
			}
		}
		return path
	}
	runner := *t
	runner.repositoryRoot, runner.configurationRoot, runner.isolated = rebase(t.repositoryRoot), target, true
	runner.command, runner.args = rebase(t.command), slices.Clone(t.args)
	for i, arg := range runner.args {
		if option, value, assigned := strings.Cut(arg, "="); assigned && strings.HasPrefix(option, "-") {
			runner.args[i] = option + "=" + rebase(value)
		} else {
			runner.args[i] = rebase(arg)
		}
	}
	runner.configurationEnvironment = isolatedCIEnvironment(t.configurationEnvironment, source, target)
	runner.executor = configurationExecutor{commandExecutor: t.executor, environment: runner.configurationEnvironment}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(runner.repositoryRoot); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(cwd)) }()
	probePath, err := appendVitestProbe(source, target, baseline)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	_, projects, files, err := runner.inspectVitest(ctx, session.Directory(), probePath)
	if runner.usesVitestRunner() {
		files = slices.DeleteFunc(files, func(file string) bool { return filepath.Clean(file) != filepath.Clean(probePath) })
	}
	if err != nil || len(projects) != 1 || len(files) != 1 || filepath.Clean(files[0]) != filepath.Clean(probePath) {
		unavailableFeatures(result, fmt.Sprintf("The existing probe file was not uniquely discovered in the disposable checkout: %v", err))
		return nil
	}
	return runner.runSelectedVitestProbe(ctx, output, session, preload, probePath, result)
}

func appendVitestProbe(source, target string, baseline validationRun) (string, error) {
	source, target = workspace.CanonicalRoot(source), workspace.CanonicalRoot(target)
	if relative, err := filepath.Rel(source, target); err != nil || relative == "." || filepath.IsLocal(relative) {
		return "", fmt.Errorf("vitest probe copy must be outside the original checkout")
	}
	for _, test := range baseline.Tests {
		if test.Status != "passed" && test.Status != "failed" {
			continue
		}
		file, err := filepath.EvalSymlinks(test.File)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(source, file)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		probe := filepath.Join(target, relative)
		resolved, err := filepath.EvalSymlinks(probe)
		if err != nil {
			return "", err
		}
		if relative, err := filepath.Rel(target, resolved); err != nil || !filepath.IsLocal(relative) {
			return "", fmt.Errorf("copied probe file escapes its disposable checkout")
		}
		handle, err := os.OpenFile(resolved, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return "", err
		}
		// A block keeps the probe's bindings separate from the existing module.
		probeSource := strings.Replace(vitestProbeSource, "import {test} from 'vitest';", "const {test} = await import('vitest');", 1)
		_, writeErr := io.WriteString(handle, "\n;{\n"+probeSource+"\n}\n")
		return probe, errors.Join(writeErr, handle.Close())
	}
	return "", fmt.Errorf("no executed test file inside the checkout is available for an isolated Vitest probe")
}
