// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeReviewJenkinsLexicalRuntime(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, tc := range []struct{ outer, override, inherited string }{
		{"tools { nodejs '24.1.0' }", "tools { nodejs '18.20.8' }", "24.1.0"},
		{"agent { docker { image 'node:24.1.0' } }", "agent { docker { image 'node:16.20.2' } }", "24.1.0"},
		{"agent any", "tools { nodejs '18.20.8' }", ""},
	} {
		t.Run(tc.outer+tc.override, func(t *testing.T) {
			root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { " + tc.outer + "; stages { stage('Override') { " + tc.override + "; steps { sh 'npm test' } }; stage('Inherited') { steps { sh 'npm test' } } } }"})
			workflows, err := readWorkflows(root)
			require.NoError(t, err)
			job := workflows[0].Jobs["pipeline"]
			require.Len(t, job.Steps, 2)
			require.Equal(t, tc.inherited, job.Steps[1].Native.NodeVersion)
			node, err := rowSetupNodeBefore(root, workflows[0], job, nil, 1)
			require.NoError(t, err)
			require.Equal(t, tc.inherited, node)
		})
	}
}

func TestNativeReviewJenkinsInterpolationAndComputedArguments(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	root := providerFixture(t, map[string]string{"Jenkinsfile": `pipeline { tools { nodejs '24.1.0' }; environment { TEST_SCRIPT = 'test' }; stages { stage('Test') { steps { sh "npm run ${env.TEST_SCRIPT}" } } } }`})
	plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	require.Equal(t, "npm run test", workflows[0].Jobs["pipeline"].Steps[0].Run)
	for _, command := range []string{`sh('npm test ' + params.TEST_ARGS)`, `sh(script: 'npm test ' + params.TEST_ARGS)`, `sh 'npm test ' + params.TEST_ARGS`, "sh('npm test'\n * params.REPEAT)", `sh('npm test'.trim())`} {
		t.Run(command, func(t *testing.T) {
			text := "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { " + command + " } } } }"
			root := providerFixture(t, map[string]string{"Jenkinsfile": text})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "computed")
			actual, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
			require.NoError(t, err)
			require.Equal(t, text, string(actual))
		})
	}
}

func TestNativeReviewRuntimeSwitchAndOptOut(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script: [nvm install 18.20.8]\n script: npm test\n"})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "18.20.8")
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
	}, "jest")
	require.Len(t, check.Jobs, 1)
	require.Equal(t, "18.20.8", check.Jobs[0].Node)
	plan, err := PlanJavaScriptSetup(root, "jest", "5.99.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	check = checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.99.0", Node: ">=18"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, check.Status, "%+v", check)
	require.Equal(t, "18.20.8", check.Jobs[0].Node)
	check = checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.99.0", Node: ">=20"}, nil
	}, "jest")
	require.Equal(t, StatusIncompatible, check.Status, "%+v", check)
	text := "disabled:\n image: node:24.1.0\n variables: {DD_CIVISIBILITY_ENABLED: 'false'}\n script: npm test\nenabled:\n image: node:24.1.0\n script: npm test\n"
	root = providerFixture(t, map[string]string{".gitlab-ci.yml": text})
	plan, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	w, err := readWorkflows(root)
	require.NoError(t, err)
	require.Empty(t, w[0].Jobs["disabled"].Steps[0].Native.Tracer)
	require.Equal(t, "6.20.1", w[0].Jobs["enabled"].Steps[0].Native.Tracer)
	check = checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
	}, "jest")
	for _, finding := range check.Jobs {
		if finding.Job == "disabled" {
			require.Equal(t, StatusExcluded, finding.Status)
		}
	}
}

func TestNativeReviewSharedScriptsProtectEveryConsumer(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, variables := range []string{
		"parallel: {matrix: [{NODE_VERSION: ['16.20.2', '24.1.0']}]}\n",
		"parallel: {matrix: [{NODE_VERSION: ['18.20.8', '24.1.0']}]}\n",
		"variables: {NODE_VERSION: '24.1.0'}\n parallel: {matrix: [{DD_CIVISIBILITY_ENABLED: ['false', 'true']}]}\n",
	} {
		text := "test:\n image: node:$NODE_VERSION\n " + variables + " script: npm test\n"
		root := providerFixture(t, map[string]string{".gitlab-ci.yml": text})
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.Error(t, err, text)
		actual, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
		require.NoError(t, err)
		require.Equal(t, text, string(actual))
	}
}

func TestNativeReviewCommonJSAndESMImportFloor(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:18.17.0\n script: npm test\n"})
	plan, err := PlanJavaScriptSetup(root, "jest", "5.99.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	diff, err := plan.Diff()
	require.NoError(t, err)
	require.NotContains(t, diff, "--import")
	_, err = plan.Apply(true)
	require.NoError(t, err)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.99.0", Node: ">=18"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, check.Status, "%+v", check)
	esmRoot := providerFixture(t, map[string]string{
		".gitlab-ci.yml": "test:\n image: node:18.17.0\n script: npm test\n",
		"package.json":   `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"2.1.9"}}`,
	})
	_, err = PlanJavaScriptSetup(esmRoot, "vitest", "5.99.0", "datadoghq.com", ">=18")
	require.ErrorContains(t, err, "ESM import runtime")
}

func TestNativeReviewMochaCoverage(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{
		".gitlab-ci.yml": "test:\n image: node:24.1.0\n script: npm test\n",
		"package.json":   `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6"}}`,
	})
	_, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	var missing *SetupDependencyGap
	require.ErrorAs(t, err, &missing)
	require.Contains(t, missing.Packages, "nyc")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0"}}`), 0600))
	plan, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	diff, err := plan.Diff()
	require.NoError(t, err)
	require.Contains(t, diff, "npx --no-install nyc -- npm test")
	_, err = plan.Apply(true)
	require.NoError(t, err)
	w, err := readWorkflows(root)
	require.NoError(t, err)
	require.Equal(t, "npx --no-install nyc -- npm test", w[0].Jobs["test"].Steps[0].Run)
	second, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	require.True(t, second.Configured())
}

func TestNativeReviewCucumberESMCoverage(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{
		".gitlab-ci.yml": "test:\n image: node:24.1.0\n script: npm test\n",
		"package.json":   `{"type":"module","scripts":{"test":"cucumber-js"},"devDependencies":{"@cucumber/cucumber":"13.2.1","nyc":"17.1.0","@istanbuljs/esm-loader-hook":"0.3.0"}}`,
	})
	plan, err := PlanJavaScriptSetup(root, "cucumber", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	w, err := readWorkflows(root)
	require.NoError(t, err)
	require.Contains(t, w[0].Jobs["test"].Steps[0].Native.Original, "--experimental-loader @istanbuljs/esm-loader-hook")
	second, err := PlanJavaScriptSetup(root, "cucumber", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	require.True(t, second.Configured())

}

func TestNativeReviewRejectsSharedShellMutation(t *testing.T) {
	for _, provider := range []string{"gitlab", "buildkite"} {
		t.Run(provider, func(t *testing.T) {
			selectProviderForTest(t, provider)
			path, contents := ".gitlab-ci.yml", "test:\n image: node:24.1.0\n script: ['cd package && npm test', 'npm run lint']\n"
			if provider == "buildkite" {
				path, contents = ".buildkite/pipeline.yml", "image: node:24.1.0\nsteps:\n - commands: ['cd package && npm test', 'npm run lint']\n"
			}
			root := providerFixture(t, map[string]string{path: contents, "package/package.json": `{"scripts":{"test":"jest","lint":"eslint"},"devDependencies":{"jest":"30.5.1"}}`})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "shell state")
			actual, err := os.ReadFile(filepath.Join(root, path))
			require.NoError(t, err)
			require.Equal(t, contents, string(actual))
		})
	}
}

func TestNativeReviewBuildkiteRuntimeEscaping(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": "image: node:24.1.0\nsteps:\n - command: npm test\n"})
	plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	w, err := readWorkflows(root)
	require.NoError(t, err)
	for _, job := range w[0].Jobs {
		source := job.Steps[0].Native.Original
		require.Contains(t, source, "$${DD_API_KEY:-}")
		require.Contains(t, source, "$$_ddtest_tracer_dir")
		require.NotContains(t, strings.ReplaceAll(source, "$$", ""), "$")
		require.Equal(t, "npm test", job.Steps[0].Run)
	}
	second, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	require.True(t, second.Configured())
	setup := nativeSetup{Framework: "jest", Version: "6.20.1", Site: "datadoghq.com"}
	escaped := wrapNativeCommand(setup, "npm test -- '$$ORIGINAL'", "buildkite")
	_, original, ok := unwrapNativeInstrumentation(escaped, "buildkite")
	require.True(t, ok)
	require.Equal(t, "npm test -- '$$ORIGINAL'", original)
	_, _, ok = unwrapNativeInstrumentation(escaped, "gitlab")
	require.False(t, ok, "Buildkite's escaped recipe is invalid in other providers")
}

func TestNativeReviewBootstrapExecutesProviderPreloads(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	for _, tc := range []struct {
		provider, framework, expected string
		esm, disabled                 bool
	}{
		{"gitlab", "jest", "true undefined", false, false},
		{"buildkite", "jest", "true undefined", false, false},
		{"gitlab", "vitest", "true true", true, false},
		{"buildkite", "vitest", "true true", true, false},
		{"gitlab", "jest", "undefined undefined", false, true},
		{"buildkite", "jest", "undefined undefined", false, true},
	} {
		t.Run(tc.provider+"/"+tc.framework, func(t *testing.T) {
			root := t.TempDir()
			tools, cache := filepath.Join(root, "tools"), filepath.Join(root, "cache with spaces")
			require.NoError(t, os.MkdirAll(tools, 0700))
			require.NoError(t, os.MkdirAll(cache, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(tools, "npm"), []byte(`#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--prefix" ]; then shift; prefix=$1; fi
 shift
done
mkdir -p "$prefix/node_modules/dd-trace/ci"
printf 'global.__ciRequire = true;' > "$prefix/node_modules/dd-trace/ci/init.js"
printf 'global.__ciImport = true;' > "$prefix/node_modules/dd-trace/register.js"
`), 0700))
			original := `"` + node + `" -e 'console.log(global.__ciRequire, global.__ciImport)'`
			command := wrapNativeCommand(nativeSetup{Framework: tc.framework, Version: "6.20.1", Site: "datadoghq.com", ESM: tc.esm}, original, tc.provider)
			if tc.provider == "buildkite" {
				// Simulate the documented pipeline-upload unescape. The source
				// escaping assertion above rejects unescaped runtime references.
				command = strings.ReplaceAll(command, "$$", "$")
			}
			cmd := exec.Command("sh", "-c", command)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+cache, "DD_API_KEY=synthetic", "NODE_OPTIONS=")
			if tc.disabled {
				cmd.Env = append(cmd.Env, "DD_CIVISIBILITY_ENABLED=false")
			} else {
				cmd.Env = append(cmd.Env, "DD_CIVISIBILITY_ENABLED=true")
			}
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Equal(t, tc.expected, strings.TrimSpace(string(output)))
			entries, err := os.ReadDir(cache)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}
