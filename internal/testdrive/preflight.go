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
	"path/filepath"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/onboard"
)

// jestPreflight records configuration and runtime evidence for any framework.
type jestPreflight struct {
	// Runtime is the local runtime version, for languages other than Node.
	Runtime             string                       `json:"runtime,omitempty"`
	WebdriverIO         *framework.WebdriverIOConfig `json:"webdriverio,omitempty"`
	RunnerBinary        *runnerBinary                `json:"runner_binary,omitempty"`
	PlaywrightProjects  []playwrightProject          `json:"playwright_projects,omitempty"`
	VitestProjects      []vitestProject              `json:"vitest_projects,omitempty"`
	ExecutionPath       string                       `json:"execution_path,omitempty"`
	Verdict             verdict                      `json:"verdict"`
	Command             string                       `json:"command"`
	Node                string                       `json:"node"`
	Executable          string                       `json:"executable,omitempty"`
	Prefix              string                       `json:"prefix,omitempty"`
	Arguments           []string                     `json:"arguments,omitempty"`
	Version             string                       `json:"version"`
	Projects            []jestProject                `json:"projects"`
	Recommendation      *tracerRecommendation        `json:"recommendation,omitempty"`
	RecommendationNotes []string                     `json:"recommendation_notes,omitempty"`
}

type preflightCheck = jestPreflight

func (t *Testdrive) runConfigurationChecks(ctx context.Context, output io.Writer) (runErr error) {
	result := validationResult{Framework: t.framework.Name(), Tracer: t.tracerLabel, CheckOnly: true,
		Compatibility: verdict{Status: onboard.StatusNotExercised, Reason: "Configuration checks only; run testdrive without --check-only for paired execution."},
		Features:      []featureResult{{Name: "all", Status: onboard.StatusNotExercised, Reason: "Configuration checks only."}}}
	defer func() { runErr = errors.Join(runErr, finishValidation(output, t.repositoryRoot, result)) }()
	if t.discoveryError != nil {
		result.Error = t.discoveryError.Error()
		return t.discoveryError
	}
	scope, err := onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
	if err != nil {
		result.Error = err.Error()
		return err
	}
	result.Scope = &scope
	check := checkConfigurationCIRuntimes(ctx, t.repositoryRoot, t.framework.Name())
	result.CIRuntime = &check
	t.defaultToCITracer(check)
	result.Tracer = t.tracerLabel
	preflight := t.frameworkPreflight()
	if preflight == nil {
		err := fmt.Errorf("configuration checks are not supported for %s", t.framework.Name())
		result.Error = err.Error()
		return err
	}
	if t.lang().isolatesConfigurations() && !t.isolated {
		// Loading the project's dependencies can run project code even when
		// nothing is installed; focused checks get the same copy as --all.
		runner := *t
		runner.preflight = preflight
		item := configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Environment: t.configurationEnvironment}}
		checked, err := runner.runPreparedConfiguration(ctx, output, &item)
		result.Preflight, result.CIRuntime, result.Tracer = checked.Preflight, checked.CIRuntime, checked.Tracer
		if err != nil {
			result.Error = err.Error()
		}
		return err
	}
	if err := preflight(ctx, output, &result); err != nil {
		result.Error = err.Error()
		return err
	}
	return nil
}

// Full package/checkout scope coordination belongs to the configuration PR.
// Until then, nested invocation must not certify CI it did not inspect.
func checkConfigurationCIRuntimes(ctx context.Context, packageRoot, frameworkName string) onboard.RuntimeCheck {
	check := frameworkCIRuntimes(ctx, packageRoot, frameworkName)
	checkout := git.GetSourceRoot()
	if checkout == "" {
		return check
	}
	canonicalPackage, _ := filepath.EvalSymlinks(packageRoot)
	canonicalCheckout, _ := filepath.EvalSymlinks(checkout)
	if canonicalPackage == canonicalCheckout {
		return check
	}
	// The selected provider's checkout-level configuration, not another's.
	files, err := onboard.CIProviderConfigurationFiles(checkout)
	if err != nil || len(files) > 0 {
		location := onboard.CIProviderDisplayName()
		if len(files) > 0 {
			location = filepath.ToSlash(filepath.Dir(files[0]))
		}
		check.Status = onboard.StatusInconclusive
		check.Reason = "Checkout-level " + onboard.CIProviderDisplayName() + " workflows were not validated from this package directory. Validate the checkout's CI/package scope before claiming configuration checks passed."
		check.Review = append(check.Review, onboard.RuntimeFinding{Workflow: location, Status: onboard.StatusNotExercised, Code: onboard.CodeNestedCIScope, Reason: check.Reason})
	}
	return check
}
