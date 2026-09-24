// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
)

type jestProject struct {
	Runner      string `json:"testRunner"`
	Environment string `json:"testEnvironment"`
	Root        string `json:"rootDir"`
}

type jestPreflight struct {
	Verdict  verdict       `json:"verdict"`
	Command  string        `json:"command"`
	Node     string        `json:"node"`
	Version  string        `json:"version"`
	Projects []jestProject `json:"projects"`
}

func (t *Testdrive) checkJestPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	args := appendJestArgs(t.command, t.args, "--showConfig")
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Effective Jest configuration was not resolved."}}
	result.Preflight = check
	_, _ = fmt.Fprintf(output, "Inspecting Jest configuration: %s\n", check.Command)
	// Loading Jest configuration may execute project JavaScript. This belongs in
	// the approved execution phase, never detection or preview. Disable uploads.
	env := testEnvironment("", "http://127.0.0.1:1", "preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
	env["DD_CIVISIBILITY_ENABLED"] = "false"
	env["DD_TRACE_ENABLED"] = "false"
	data, err := t.executor.CombinedOutput(ctx, t.command, args, env)
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
		if b == '{' && json.NewDecoder(strings.NewReader(string(data[i:]))).Decode(&config) == nil && config.Version != "" && len(config.Configs) > 0 {
			decoded = true
			break
		}
	}
	if !decoded {
		return fmt.Errorf("could not read Jest --showConfig; pass the actual Jest command including its config with --command")
	}
	check.Version = config.Version
	check.Projects = config.Configs
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("jest preflight requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
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
	if check.Verdict.Status == "incompatible" {
		return fmt.Errorf("jest preflight: %s", check.Verdict.Reason)
	}
	if check.Verdict.Status != "compatible" && (!strings.HasPrefix(selection.Requested, "git:") || t.checkOnly) {
		return fmt.Errorf("jest preflight is inconclusive: %s", check.Verdict.Reason)
	}
	return nil
}

// Supported major lines mirror dd-trace-js's Jest instrumentation hooks. Keep
// this small table covered by boundary tests; unknown majors require review.
// Node engines alone do not describe framework/runner compatibility.
func checkJestSupport(check jestPreflight, selection platform.JSSelection) verdict {
	var problems []string
	for _, project := range check.Projects {
		runner := filepath.ToSlash(project.Runner)
		if !strings.Contains(runner, "/jest-circus/") && runner != "jest-circus/runner" {
			problems = append(problems, "Jest requires the jest-circus runner for Test Optimization; add the matching jest-circus version and set testRunner to 'jest-circus/runner', or choose a supported Jest upgrade")
		}
	}
	major, minor, ok := releaseParts(check.Version)
	tracerMajor, _, known := releaseParts(selection.Version)
	if known && (tracerMajor == 5 || tracerMajor == 6) && ok {
		minimum := 24
		if tracerMajor == 6 {
			minimum = 28
		}
		if major < minimum || (major == 24 && minor < 8) {
			problems = append(problems, fmt.Sprintf("dd-trace %d requires Jest >=%s; detected %s. Select a supported tracer with --tracer-version or review a Jest upgrade", tracerMajor, map[int]string{5: "24.8", 6: "28"}[tracerMajor], check.Version))
		}
	}
	if len(problems) > 0 {
		return verdict{Status: "incompatible", Reason: strings.Join(problems, ". ")}
	}
	if !ok || !known || (tracerMajor != 5 && tracerMajor != 6) {
		return verdict{Status: "inconclusive", Reason: "No verified Jest compatibility rule for this tracer/framework version; source builds are checked again after installation."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "Jest version, Circus runner, and local Node satisfy the known prerequisites. Paired execution and feature checks are still required."}
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

func verifyInstalledSelection(preload string, result *validationResult) error {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(preload)), "package.json"))
	if err != nil {
		return err
	}
	var manifest struct {
		Version string `json:"version"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if result.Selection.Version != "" && manifest.Version != result.Selection.Version {
		return fmt.Errorf("installed dd-trace %s differs from preflight selection %s", manifest.Version, result.Selection.Version)
	}
	result.Selection.Version = manifest.Version
	result.Selection.Node = manifest.Engines.Node
	result.Tracer = "dd-trace@" + manifest.Version
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, manifest.Version)
	}
	return nil
}
