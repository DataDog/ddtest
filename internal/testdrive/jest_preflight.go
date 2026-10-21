// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/Masterminds/semver/v3"
	"github.com/kballard/go-shellquote"
)

type jestProject struct {
	ID          string `json:"id,omitempty"`
	DisplayName struct {
		Name string `json:"name"`
	} `json:"displayName"`
	Runner                 string   `json:"testRunner"`
	Environment            string   `json:"testEnvironment"`
	Root                   string   `json:"rootDir"`
	InjectGlobals          *bool    `json:"injectGlobals,omitempty"`
	ExtensionsToTreatAsEsm []string `json:"extensionsToTreatAsEsm,omitempty"`
}

type jestPreflight struct {
	Verdict             verdict               `json:"verdict"`
	Command             string                `json:"command"`
	Node                string                `json:"node"`
	Executable          string                `json:"executable,omitempty"`
	Arguments           []string              `json:"arguments,omitempty"`
	Version             string                `json:"version"`
	Projects            []jestProject         `json:"projects"`
	Recommendation      *tracerRecommendation `json:"recommendation,omitempty"`
	RecommendationNotes []string              `json:"recommendation_notes,omitempty"`
}

func (t *Testdrive) checkJestPreflight(ctx context.Context, output io.Writer, result *validationResult) (preflightErr error) {
	command, baseArgs := t.command, t.args
	if slices.Contains([]string{"npm", "npx", "pnpm", "yarn", "bun"}, filepath.Base(command)) {
		if _, _, err := t.installedJest(ctx); err != nil {
			result.Preflight = &jestPreflight{Verdict: verdict{Status: onboard.StatusInconclusive, Code: onboard.CodeJestNotInstalled, Reason: err.Error()}}
			return err
		}
	}
	if command == "npx" {
		baseArgs = append([]string{"--no-install"}, baseArgs...)
	}
	if command == "jest" {
		var err error
		command, baseArgs, err = t.installedJest(ctx)
		if err != nil {
			result.Preflight = &jestPreflight{Verdict: verdict{Status: onboard.StatusInconclusive, Code: onboard.CodeJestNotInstalled, Reason: err.Error()}}
			return err
		}
	}
	args := framework.WithFrameworkOptions(command, baseArgs, "jest", "--showConfig", "--watchman=false")
	check := &jestPreflight{Command: shellquote.Join(append([]string{command}, args...)...), Verdict: verdict{Status: onboard.StatusInconclusive, Reason: "Effective Jest configuration was not resolved."}}
	result.Preflight = check
	_, _ = fmt.Fprintf(output, "Inspecting Jest configuration: %s\n", check.Command)
	// Loading Jest configuration may execute project JavaScript. This belongs in
	// the approved execution phase, never detection or preview. Disable uploads.
	env := testEnvironment("http://127.0.0.1:1", "preflight")
	for key, value := range t.preflightEnvironment {
		env[key] = value
	}
	options := os.Getenv("NODE_OPTIONS")
	if explicit, ok := t.preflightEnvironment["NODE_OPTIONS"]; ok {
		options = explicit
	}
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(options)
	directory, capture, err := prepareJestRuntimeCapture(env)
	if err != nil {
		return err
	}
	defer func() { preflightErr = errors.Join(preflightErr, os.RemoveAll(directory)) }()
	env["DD_CIVISIBILITY_ENABLED"] = "false"
	env["DD_TRACE_ENABLED"] = "false"
	data, err := t.executor.CombinedOutput(ctx, command, args, env)
	if err != nil {
		return fmt.Errorf("inspect Jest configuration: %w; verify --command includes the repository's Jest config and required setup", err)
	}
	var config struct {
		Version string        `json:"version"`
		Configs []jestProject `json:"configs"`
	}
	// npm/yarn may print a script banner before Jest's JSON.
	decoded := false
	for i, b := range data {
		if b == '{' && json.NewDecoder(bytes.NewReader(data[i:])).Decode(&config) == nil && config.Version != "" && len(config.Configs) > 0 {
			decoded = true
			break
		}
	}
	if !decoded {
		return fmt.Errorf("could not read Jest --showConfig; pass the actual Jest command including its config with --command")
	}
	runtime, err := readJestRuntimeCapture(capture)
	if err != nil {
		return err
	}
	check.Node, check.Executable, check.Arguments = runtime.Node, runtime.Executable, runtime.Arguments
	check.Version = config.Version
	check.Projects = config.Configs
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("jest preflight requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
		var metadata *platform.MetadataError
		if errors.As(err, &metadata) {
			check.Verdict.Code = metadata.Code
		}
		check.Verdict.Reason = "Jest configuration resolved, but tracer selection could not be resolved: " + reportText(err.Error())
		return err
	}
	result.Tracer = "dd-trace@" + selection.Version
	result.TracerSource = selection.Source
	if selection.Source == "project" {
		_, _ = fmt.Fprintln(output, "Using the existing project tracer; --tracer-version applies only when it is absent.")
	}
	check.Verdict = checkJestSupport(*check, selection)
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	}
	_, _ = fmt.Fprintf(output, "Jest %s; Node %s; tracer %s (%s; requested %s)\nPreflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, selection.Requested, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status == onboard.StatusIncompatible {
		return fmt.Errorf("jest preflight: %s", check.Verdict.Reason)
	}
	if check.Verdict.Status != onboard.StatusCompatible && (!strings.HasPrefix(selection.Requested, "git:") || t.checkOnly) {
		return fmt.Errorf("jest preflight is inconclusive: %s", check.Verdict.Reason)
	}
	return nil
}

// Supported major lines mirror dd-trace-js's Jest instrumentation hooks. Keep
// this small table covered by boundary tests; unknown majors require review.
// Node engines alone do not describe framework/runner compatibility.
var jestTracerMinimums = map[int]string{5: "24.8", 6: "28"}

func checkJestSupport(check jestPreflight, selection platform.JSSelection) verdict {
	var problems []string
	for _, project := range check.Projects {
		runner := filepath.ToSlash(project.Runner)
		if !strings.Contains(runner, "/jest-circus/") && runner != "jest-circus/runner" {
			problems = append(problems, "Jest requires the jest-circus runner for Test Optimization; add the matching jest-circus version and set testRunner to 'jest-circus/runner', or choose a supported Jest upgrade")
		}
	}
	jestVersion, err := semver.StrictNewVersion(strings.TrimPrefix(check.Version, "v"))
	tracerMajor, _, known := releaseParts(selection.Version)
	minimum, verified := jestTracerMinimums[tracerMajor]
	if !known || !verified {
		return verdict{Status: onboard.StatusInconclusive, Code: onboard.CodeUnverifiedTracerMajor, Reason: fmt.Sprintf("Unverified tracer major for dd-trace@%s; no Jest compatibility rule is available.", selection.Version)}
	}
	if err != nil {
		return verdict{Status: onboard.StatusInconclusive, Code: onboard.CodeUnresolvedJestVersion, Reason: "Could not resolve the installed Jest release."}
	}
	constraint, _ := semver.NewConstraint(">=" + minimum)
	if !constraint.Check(jestVersion) {
		problems = append(problems, fmt.Sprintf("dd-trace %d requires Jest >=%s; detected %s", tracerMajor, minimum, check.Version))
	}
	if len(problems) > 0 {
		return verdict{Status: onboard.StatusIncompatible, Code: onboard.CodeUnsupportedJestConfiguration, Reason: strings.Join(problems, ". ")}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != onboard.StatusCompatible {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: onboard.StatusCompatible, Reason: "Jest version, Circus runner, and local Node satisfy the known prerequisites. Paired execution and feature checks are still required."}
}

func releaseParts(version string) (int, int, bool) {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 || strings.ContainsAny(version, "-+") {
		return 0, 0, false
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	_, e3 := strconv.Atoi(parts[2])
	return major, minor, e1 == nil && e2 == nil && e3 == nil
}

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
	if err := t.checkJestPreflight(ctx, output, &result); err != nil {
		result.Error = err.Error()
		return err
	}
	return nil
}

// Full package/checkout scope coordination belongs to the configuration PR.
// Until then, nested invocation must not certify CI it did not inspect.
func checkConfigurationCIRuntimes(ctx context.Context, packageRoot, frameworkName string) onboard.RuntimeCheck {
	check := onboard.CheckCIRuntimes(ctx, packageRoot, frameworkName)
	checkout := git.GetSourceRoot()
	if checkout == "" {
		return check
	}
	canonicalPackage, _ := filepath.EvalSymlinks(packageRoot)
	canonicalCheckout, _ := filepath.EvalSymlinks(checkout)
	if canonicalPackage == canonicalCheckout {
		return check
	}
	entries, err := os.ReadDir(filepath.Join(checkout, ".github", "workflows"))
	if os.IsNotExist(err) {
		return check
	}
	hasWorkflow := err != nil
	for _, entry := range entries {
		hasWorkflow = hasWorkflow || (!entry.IsDir() && (filepath.Ext(entry.Name()) == ".yml" || filepath.Ext(entry.Name()) == ".yaml"))
	}
	if hasWorkflow {
		check.Status = onboard.StatusInconclusive
		check.Reason = "Checkout-level GitHub Actions workflows were not validated from this package directory. Validate the checkout's CI/package scope before claiming configuration checks passed."
		check.Review = append(check.Review, onboard.RuntimeFinding{Workflow: ".github/workflows", Status: onboard.StatusNotExercised, Code: onboard.CodeNestedCIScope, Reason: check.Reason})
	}
	return check
}

func verifyInstalledSelection(ctx context.Context, preload string, result *validationResult) error {
	if result.Selection == nil {
		return fmt.Errorf("tracer selection metadata is missing")
	}
	var selected platform.JSSelection
	var err error
	if result.Selection.Source == "project" {
		selected, err = platform.NewJavaScript().ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: result.Selection.Requested})
	} else {
		data, readErr := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(preload)), "package.json"))
		err = readErr
		if err == nil {
			var manifest struct {
				Version string `json:"version"`
				Engines struct {
					Node string `json:"node"`
				} `json:"engines"`
			}
			err = json.Unmarshal(data, &manifest)
			selected.Version, selected.Node = manifest.Version, manifest.Engines.Node
		}
	}
	if err != nil {
		return fmt.Errorf("read installed tracer metadata: %w", err)
	}
	if selected.Version == "" || selected.Node == "" {
		return fmt.Errorf("installed tracer metadata has no version or Node requirement")
	}
	if result.Selection.Version != "" && selected.Version != result.Selection.Version {
		return fmt.Errorf("installed dd-trace %s differs from preflight selection %s", selected.Version, result.Selection.Version)
	}
	result.Selection.Version, result.Selection.Node = selected.Version, selected.Node
	result.Tracer = "dd-trace@" + selected.Version
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selected.Version)
	}
	return nil
}
