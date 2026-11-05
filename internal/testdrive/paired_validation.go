// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"fmt"
	"io"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

type pairedFramework struct {
	// run executes one baseline, reporting-only or probe variant.
	run func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, preload string, spec runSpec) (validationRun, error)
	// features runs the feature probes after a compatible pair.
	features func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, preload string, baseline validationRun, result *validationResult) error
	// preflight checks the framework's own prerequisites; nil has none.
	preflight func(t *Testdrive, ctx context.Context, output io.Writer, result *validationResult) error
	// telemetry matches native results to reported telemetry; nil uses
	// defaultTelemetry.
	telemetry func(validationRun) verdict
	// probeTelemetry matches a probe control's native result to its
	// telemetry; nil uses the shared file-based check.
	probeTelemetry func(validationRun) verdict
	// preflightAfterInstall runs preflight again once the tracer is
	// installed, to inspect the dependencies that load it.
	preflightAfterInstall bool
	// verifyInstallation confirms that the selected runtime loads the selected tracer.
	verifyInstallation func(t *Testdrive, ctx context.Context, preload string) error
	// missingCoverage is the blocking check when real-suite coverage is
	// missing; empty uses the default.
	missingCoverage string
	// outcomeTotals adds the reporting-only native outcomes summed across
	// configurations to the handoff.
	outcomeTotals bool
}

// pairedFrameworkNamed returns the paired-validation adapter of framework.
func pairedFrameworkNamed(framework string) (pairedFramework, bool) {
	language := languageForFramework(framework)
	if language == nil {
		return pairedFramework{}, false
	}
	adapter, ok := language.pairedFrameworks()[framework]
	return adapter, ok
}

func (t *Testdrive) frameworkPreflight() func(context.Context, io.Writer, *validationResult) error {
	if adapter, ok := pairedFrameworkNamed(t.framework.Name()); ok && adapter.preflight != nil {
		return func(ctx context.Context, output io.Writer, result *validationResult) error {
			return adapter.preflight(t, ctx, output, result)
		}
	}
	return nil
}

func (t *Testdrive) runFrameworkValidation(ctx context.Context, output io.Writer, session *workspace.Session, preload string, result *validationResult) error {
	adapter, ok := pairedFrameworkNamed(t.framework.Name())
	if !ok {
		return fmt.Errorf("paired validation is not supported for %s", t.framework.Name())
	}
	run := func(ctx context.Context, output io.Writer, session *workspace.Session, preload string, spec runSpec) (validationRun, error) {
		value, err := adapter.run(t, ctx, output, session, preload, spec)
		return markTimeout(value), err
	}
	baseline, err := run(ctx, output, session, preload, runSpec{Name: "baseline", Role: roleBaseline, Instrumented: false})
	result.Runs = append(result.Runs, baseline.summary())
	if err != nil {
		return err
	}
	instrumented, err := run(ctx, output, session, preload, runSpec{Name: "reporting-only", Role: roleReporting, Instrumented: true})
	result.Runs = append(result.Runs, instrumented.summary())
	if err != nil {
		return err
	}
	if err := t.writeHTMLReport(output, result, instrumented); err != nil {
		return err
	}
	telemetry := adapter.telemetry
	if telemetry == nil {
		telemetry = verifyJestTelemetry
	}
	result.Compatibility = compareRuns(baseline, instrumented, telemetry)
	if result.Compatibility.Status == "suspected regression" {
		baselineAgain, err := run(ctx, output, session, preload, runSpec{Name: "baseline-repeat", Role: roleRepeat, Instrumented: false})
		result.Runs = append(result.Runs, baselineAgain.summary())
		if err != nil {
			return err
		}
		instrumentedAgain, err := run(ctx, output, session, preload, runSpec{Name: "reporting-only-repeat", Role: roleRepeat, Instrumented: true})
		result.Runs = append(result.Runs, instrumentedAgain.summary())
		if err != nil {
			return err
		}
		if err := t.writeHTMLReport(output, result, instrumentedAgain); err != nil {
			return err
		}
		result.Compatibility = compareRepeatedRuns(baseline, instrumented, baselineAgain, instrumentedAgain, telemetry)
	}
	// Probes would only repeat the hang; name the timeout once instead.
	if reason := pairTimeout(baseline, instrumented); reason != "" {
		unavailableFeatures(result, reason)
		return nil
	}
	if compatibility, feature, ok := missingNativeResults(baseline); ok {
		result.Features = []featureResult{{Name: "all", Status: "not exercised", Reason: feature}}
		result.Compatibility = compatibility
		return nil
	}
	return adapter.features(t, ctx, output, session, preload, baseline, result)
}

// pairTimeout explains a paired comparison that testdrive's --timeout cut short.
func pairTimeout(baseline, instrumented validationRun) string {
	if baseline.Timeout != "" {
		return baseline.Timeout + "."
	}
	if instrumented.Timeout != "" {
		return instrumented.Timeout + ", while the uninstrumented run finished."
	}
	return ""
}

// missingNativeResults explains a baseline without test results. A failed
// command is a startup/setup problem; a successful one that ddtest can't read
// is a result-reading problem and must not send agents to change the suite.
func missingNativeResults(baseline validationRun) (verdict, string, bool) {
	if len(baseline.Tests) > 0 || (baseline.ResultError == "" && baseline.ExitCode == 0) {
		return verdict{}, "", false
	}
	if baseline.ExitCode == 0 {
		return verdict{Status: "inconclusive", Code: "native_results_unreadable", Reason: reportText(baseline.ResultError) + ". The native suite exited 0, but ddtest could not read its results, so features were not validated. If this command runs the tests, this is a ddtest result-reading problem, not a suite failure: report it with the ddtest version instead of changing the repository."}, "ddtest could not read the native results of a successful suite run, so features were not validated.", true
	}
	return verdict{Status: "inconclusive", Code: "native_startup_failed", Reason: "Native execution failed before any test results. Resolve the baseline startup/setup diagnostic first: " + reportText(baseline.Diagnostic+" "+baseline.ResultError)}, "The native suite did not produce test results. Resolve its startup/setup diagnostic before feature or coverage validation.", true
}

func configureScenarioEnvironment(env map[string]string, feature string) {
	// Coverage requires ITR negotiation; tests_skipping is separately disabled by
	// the reporting-only settings response. No skippable tests are returned there.
	env["DD_CIVISIBILITY_ITR_ENABLED"] = "true"
	env["DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED"] = fmt.Sprint(feature == "early-flake-detection")
	env["DD_TEST_EARLY_FLAKE_DETECTION_RETRY_COUNT"] = fmt.Sprint(intake.ScenarioRetryCount)
	env["DD_CIVISIBILITY_FLAKY_RETRY_ENABLED"] = fmt.Sprint(feature == "auto-retries")
	env["DD_CIVISIBILITY_FLAKY_RETRY_COUNT"] = fmt.Sprint(intake.ScenarioRetryCount)
	env["DD_TEST_MANAGEMENT_ENABLED"] = fmt.Sprint(feature == "quarantine" || feature == "disabled" || feature == "attempt-to-fix")
	env["DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES"] = fmt.Sprint(intake.ScenarioRetryCount)
}
