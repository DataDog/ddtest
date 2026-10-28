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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/Masterminds/semver/v3"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestMissingJestNeverInvokesPackageDownloader(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("NODE_PATH", "")
	require.NoError(t, os.WriteFile("package.json", []byte(`{"name":"empty-project","version":"1.0.0"}`), 0600))
	viper.Reset()
	viper.Set("framework", "jest")
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	run, err := Prepare(PrepareOptions{CheckOnly: true})
	require.NoError(t, err)
	run.startIntake = func(string, intake.Scenario) (localIntake, error) {
		t.Fatal("check-only must not start intake")
		return nil, nil
	}
	require.Error(t, run.Run(t.Context(), &bytes.Buffer{}))
	var report validationResult
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &report))
	require.False(t, report.ChecksPassed)
	require.Equal(t, "jest_not_installed", report.Preflight.Verdict.Code)
	require.NoDirExists(t, filepath.Join(root, "node_modules"))
}

func TestInstalledJestResolvesWithPnPAndBinFallback(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	for _, mode := range []string{"pnp", "pnp workspace", "local bin", "missing"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("TMPDIR", t.TempDir())
			t.Setenv("NODE_PATH", "")
			require.NoError(t, os.WriteFile("package.json", []byte(`{"name":"fixture"}`), 0600))
			entry := filepath.Join(root, "cache", "jest", "bin", "jest.js")
			require.NoError(t, os.MkdirAll(filepath.Dir(entry), 0700))
			require.NoError(t, os.WriteFile(entry, []byte(`console.log('installed')`), 0600))
			switch mode {
			case "pnp", "pnp workspace":
				loader := fmt.Sprintf(`const m=require('module');const original=m._resolveFilename;m._resolveFilename=function(request,...args){if(request==='jest/bin/jest')return %q;return original.call(this,request,...args)}`, entry)
				require.NoError(t, os.WriteFile(".pnp.cjs", []byte(loader), 0600))
			case "local bin":
				require.NoError(t, os.MkdirAll("node_modules/.bin", 0700))
				require.NoError(t, os.Symlink(entry, "node_modules/.bin/jest"))
			}
			packageRoot := root
			if mode == "pnp workspace" {
				packageRoot = filepath.Join(root, "packages", "app")
				require.NoError(t, os.MkdirAll(packageRoot, 0700))
				t.Chdir(packageRoot)
			}
			run := &Testdrive{repositoryRoot: packageRoot, args: []string{"--showConfig"}, executor: &ext.DefaultCommandExecutor{}}
			command, args, err := run.installedJest(t.Context())
			if mode == "missing" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "node", command)
			require.NotContains(t, strings.Join(args, " "), "npx")
			env := map[string]string{}
			directory, capture, err := prepareJestRuntimeCapture(env)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(directory)) })
			data, err := run.executor.CombinedOutput(t.Context(), command, args, env)
			require.NoError(t, err, string(data))
			runtime, err := readJestRuntimeCapture(capture)
			require.NoError(t, err)
			require.Equal(t, []string{"--showConfig"}, runtime.Arguments)
		})
	}
}

func TestCheckOnlyReportVersionAndUnexercisedFields(t *testing.T) {
	root := t.TempDir()
	result := validationResult{CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: onboard.StatusCompatible}}}
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, result))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &fields))
	require.JSONEq(t, `1`, string(fields["schema_version"]))
	for _, name := range []string{"success", "local_success", "runs"} {
		require.NotContains(t, fields, name)
	}
}

func TestCheckOnlyBlocksRecordedPrerequisitesInReport(t *testing.T) {
	root := t.TempDir()
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Command: "jest", Prerequisites: []onboard.BuildCommand{{Command: "pnpm add -D jest@30"}}}}}
	result := validationResult{CheckOnly: true, Scope: &scope, Preflight: &jestPreflight{Verdict: verdict{Status: onboard.StatusCompatible}}}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &result))
	require.False(t, result.ChecksPassed)
}

func TestRecommendationsUseRegistryTagsAndFlagUnknownMajors(t *testing.T) {
	check := jestPreflight{Version: "30.5.1", Node: "24.14.1", Projects: []jestProject{{Runner: "jest-circus/runner"}}}
	var selectors []string
	resolve := func(_ context.Context, selector string) (platform.JSSelection, error) {
		selectors = append(selectors, selector)
		return platform.JSSelection{Version: selector, Node: ">=18"}, nil
	}
	tags := func(context.Context) (map[string]string, error) {
		return map[string]string{"latest": "7.0.0", "latest-node18": "5.132.0", "latest-node20": "5.132.0", "beta": "6.999.0"}, nil
	}
	got, _ := recommendJavaScriptTracer(t.Context(), check, nil, resolve, tags, "jest")
	require.Equal(t, []string{"7.0.0", "5.132.0"}, selectors)
	require.Equal(t, "5.132.0", got.Selection.Version)
	verdict := checkJestSupport(check, platform.JSSelection{Version: "7.0.0", Node: ">=22"})
	require.Equal(t, "unverified_tracer_major", verdict.Code)
	_, notes := recommendJavaScriptTracer(t.Context(), check, nil, resolve, func(context.Context) (map[string]string, error) { return nil, fmt.Errorf("offline") }, "jest")
	require.NotEmpty(t, notes)
}

func TestJestSupportRulesCoverCICompatibilityMatrix(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	require.NoError(t, err)
	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Jest []string `yaml:"jest"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	versions := workflow.Jobs["jest-compatibility"].Strategy.Matrix.Jest
	require.NotEmpty(t, versions)
	for major, minimum := range jestTracerMinimums {
		compatible := false
		for _, version := range versions {
			v, err := semver.StrictNewVersion(version)
			require.NoError(t, err)
			constraint, err := semver.NewConstraint(">=" + minimum)
			require.NoError(t, err)
			if constraint.Check(v) {
				compatible = true
				verdict := checkJestSupport(jestPreflight{Version: version, Node: "24.14.1", Projects: []jestProject{{Runner: "jest-circus/runner"}}}, platform.JSSelection{Version: fmt.Sprintf("%d.0.0", major), Node: ">=18"})
				require.Equal(t, onboard.StatusCompatible, verdict.Status)
			}
		}
		require.True(t, compatible, "CI must exercise each verified tracer major's supported Jest range")
	}
}

func TestNestedPackageDoesNotCertifyCheckoutCI(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git required")
	}
	root := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", root).Run())
	packageRoot := filepath.Join(root, "packages", "app")
	require.NoError(t, os.MkdirAll(packageRoot, 0700))
	t.Chdir(packageRoot)
	require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"jest"}}`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte(`jobs: {tests: {steps: [{run: 'npm test'}]}}`), 0600))
	check := checkConfigurationCIRuntimes(t.Context(), packageRoot, "jest")
	require.Equal(t, onboard.StatusInconclusive, check.Status)
	require.Equal(t, "nested_ci_scope", check.Review[0].Code)
	require.Equal(t, ".github/workflows", check.Review[0].Workflow)
	result := validationResult{CheckOnly: true, CIRuntime: &check, Preflight: &jestPreflight{Verdict: verdict{Status: onboard.StatusCompatible}}}
	require.Error(t, finishValidation(&bytes.Buffer{}, packageRoot, result))
}

func TestCheckOnlyPreparationDoesNotRunNodeStartupHooks(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("TMPDIR", t.TempDir())
	writeJestManifest(t, root)
	hook := filepath.Join(root, "startup.cjs")
	require.NoError(t, os.WriteFile(hook, []byte(`require('node:fs').writeFileSync('NODE_WAS_STARTED','bad')`), 0600))
	t.Setenv("NODE_OPTIONS", "--require "+fmt.Sprintf("%q", hook))
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	run, err := Prepare(PrepareOptions{CheckOnly: true})
	require.NoError(t, err)
	run.Preview(&bytes.Buffer{})
	require.NoFileExists(t, "NODE_WAS_STARTED")
	require.NoDirExists(t, ".testoptimization")
}

func TestFullValidationKeepsNestedCheckoutCIUnvalidated(t *testing.T) {
	run := preparedTestdrive(t)
	checkout := filepath.Dir(run.repositoryRoot)
	require.NoError(t, exec.Command("git", "init", "-q", checkout).Run())
	require.NoError(t, os.MkdirAll(filepath.Join(checkout, ".github", "workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, ".github", "workflows", "ci.yml"), []byte(`jobs: {tests: {steps: [{run: 'npm test'}]}}`), 0600))
	run.platform = &fakeTracer{err: errors.New("stop after CI inspection")}
	require.Error(t, run.Run(t.Context(), &bytes.Buffer{}))
	report := readValidationReport(t, run.repositoryRoot)
	require.NotNil(t, report.CIRuntime)
	require.Equal(t, onboard.StatusInconclusive, report.CIRuntime.Status)
	require.Equal(t, onboard.CodeNestedCIScope, report.CIRuntime.Review[0].Code)
}
