// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeRevisionInstallEnvironmentAndModes(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, tc := range []struct {
		install, before, after, dependency string
		verified                           bool
	}{
		{"npm ci", "production", "test", "devDependencies", false},
		{"yarn", "production", "test", "devDependencies", false},
		{"npm ci", "test", "production", "devDependencies", true},
		{"npm ci", "production", "test", "dependencies", true},
		{"pnpm install --prod", "production", "test", "dependencies", true},
		{"npm install --package-lock-only", "test", "test", "dependencies", false},
		{"pnpm install --lockfile-only", "test", "test", "dependencies", false},
		{"npm install --dry-run", "test", "test", "dependencies", false},
		{"npm install --dry-run=true", "test", "test", "dependencies", false},
		{"npm install --dry-run=false", "test", "test", "dependencies", true},
	} {
		t.Run(tc.install+tc.before+tc.dependency, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				"package.json":   `{"scripts":{"test":"jest"},"` + tc.dependency + `":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
				".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - export NODE_ENV=" + tc.before + "\n  - " + tc.install + "\n  - export NODE_ENV=" + tc.after + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n",
			})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			steps := w[0].Jobs["test"].Steps
			if tc.verified {
				require.Equal(t, "6.20.1", steps[len(steps)-1].Native.Tracer)
				require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
			} else {
				require.Empty(t, steps[len(steps)-1].Native.Tracer)
				require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
			}
		})
	}
}

func TestNativeRevisionExistingESMSetupRequiresImport(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	text := "test:\n image: node:24.1.0\n before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n"
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": text, "package.json": `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11","dd-trace":"6.20.1"}}`})
	_, err := PlanJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "--import")
	check := nativeFollowupCheck(t, root, "vitest")
	require.Equal(t, CodeMissingInitialization, check.Jobs[0].Code)
	actual, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
	require.NoError(t, err)
	require.Equal(t, text, string(actual))
}

func TestNativeRevisionConflictingSharedReplacements(t *testing.T) {
	selectProviderForTest(t, "circleci")
	text := "version: 2.1\ncommands:\n tests:\n  steps:\n   - run: npm test\njobs:\n cjs:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  working_directory: cjs\n  steps: [tests]\n esm:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  working_directory: esm\n  steps: [tests]\n"
	for _, esm := range []bool{false, true} {
		t.Run(map[bool]string{true: "conflicting", false: "identical"}[esm], func(t *testing.T) {
			manifest := `{"scripts":{"test":"nyc -- mocha test.js"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0"}}`
			esmManifest := manifest
			if esm {
				esmManifest = strings.Replace(manifest, "{", `{"type":"module",`, 1)
			}
			root := providerFixture(t, map[string]string{".circleci/config.yml": text, "cjs/package.json": manifest, "esm/package.json": esmManifest})
			plan, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
			if esm {
				require.ErrorContains(t, err, "different instrumentation")
				actual, err := os.ReadFile(filepath.Join(root, ".circleci/config.yml"))
				require.NoError(t, err)
				require.Equal(t, text, string(actual))
			} else {
				require.NoError(t, err)
				diff, err := plan.Diff()
				require.NoError(t, err)
				require.Equal(t, 1, strings.Count(diff, "# ddtest-ci"))
			}
		})
	}
}

func TestNativeRevisionCircleCIMatrixBlocksSharedEdits(t *testing.T) {
	selectProviderForTest(t, "circleci")
	text := "version: 2.1\njobs:\n test:\n  parameters:\n   node: {type: string, default: '24.1.0'}\n  docker: [{image: 'cimg/node:<< parameters.node >>'}]\n  steps:\n   - run: npm test\nworkflows:\n main:\n  jobs:\n   - test:\n      matrix:\n       parameters:\n        node: ['16.20.2', '24.1.0']\n"
	root := providerFixture(t, map[string]string{".circleci/config.yml": text})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "matrix")
	actual, err := os.ReadFile(filepath.Join(root, ".circleci/config.yml"))
	require.NoError(t, err)
	require.Equal(t, text, string(actual))
}

func TestNativeRevisionProviderChoiceSeesResolvedAndConditionalTests(t *testing.T) {
	selectProviderForTest(t, "")
	for _, tc := range []struct{ provider, path, text string }{
		{"gitlab", ".gitlab-ci.yml", "test:\n variables: {TEST_SCRIPT: test}\n script: npm run $TEST_SCRIPT\n"},
		{"circleci", ".circleci/config.yml", "jobs:\n test:\n  steps:\n   - when:\n      condition: '<< pipeline.parameters.run-tests >>'\n      steps:\n       - run: npm test\n"},
		{"circleci", ".circleci/config.yml", "jobs:\n test:\n  steps:\n   - unless:\n      condition: '<< pipeline.parameters.skip-tests >>'\n      steps:\n       - run: npm test\n"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			root := providerFixture(t, map[string]string{".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n", tc.path: tc.text})
			_, err := SelectCIProvider(root, "")
			var choice *CIProviderChoice
			require.ErrorAs(t, err, &choice)
			require.Equal(t, []string{"github", tc.provider}, choice.Providers)
			if tc.provider == "circleci" {
				w, err := readSelectedWorkflows(root, "circleci")
				require.NoError(t, err)
				require.Len(t, w[0].Jobs["test"].Steps, 1)
				require.NotEmpty(t, w[0].Jobs["test"].Gaps)
			}
		})
	}
}

func TestNativeRevisionCircleCIContainerEnvironment(t *testing.T) {
	selectProviderForTest(t, "circleci")
	root := providerFixture(t, map[string]string{".circleci/config.yml": "jobs:\n test:\n  parameters:\n   mode: {type: string, default: integration}\n  docker:\n   - image: cimg/node:24.1.0\n     environment: {TEST_MODE: '<< parameters.mode >>', NODE_ENV: production, OVERRIDE: container}\n  environment: {OVERRIDE: job}\n  steps:\n   - run:\n      command: npm test\n      environment: {OVERRIDE: step}\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	for key, value := range map[string]string{"TEST_MODE": "integration", "NODE_ENV": "production", "OVERRIDE": "step"} {
		require.Equal(t, value, scope.Commands[0].Environment[key], key)
	}
}

func TestNativeRevisionLiteralProviderEnvironments(t *testing.T) {
	for _, tc := range []struct{ provider, path, text string }{
		{"circleci", ".circleci/config.yml", "jobs:\n test:\n  environment: {TOKEN: blue, PATTERN: '$TOKEN'}\n  steps: [{run: npm test}]\n"},
		{"jenkins", "Jenkinsfile", "pipeline { environment { TOKEN = 'blue'; PATTERN = '$TOKEN' }; stages { stage('Test') { steps { sh 'npm test' } } } }"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			selectProviderForTest(t, tc.provider)
			root := providerFixture(t, map[string]string{tc.path: tc.text})
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "$TOKEN", scope.Commands[0].Environment["PATTERN"])
		})
	}
}

func TestNativeRevisionJenkinsAgentReplacesDockerRuntime(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, agent := range []string{"agent { label 'linux' }", "agent any", "agent { docker { image 'node:18.20.8' } }"} {
		t.Run(agent, func(t *testing.T) {
			root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { agent { docker { image 'node:24.1.0' } }; stages { stage('Override') { " + agent + "; steps { sh 'npm test' } }; stage('Inherited') { steps { sh 'npm test' } } } }"})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			steps := w[0].Jobs["pipeline"].Steps
			require.Len(t, steps, 2)
			expected := ""
			if strings.Contains(agent, "docker") {
				expected = "18.20.8"
			}
			require.Equal(t, expected, steps[0].Native.NodeVersion)
			require.Equal(t, "24.1.0", steps[1].Native.NodeVersion)
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.Error(t, err)
		})
	}
}

func TestNativeRevisionJenkinsImperativeEnvironment(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, script := range []string{`script { env.DD_CIVISIBILITY_ENABLED = 'false'; sh 'npm test' }`, `script { env.DD_CIVISIBILITY_ENABLED = 'false' }; sh 'npm test'`} {
		root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { " + script + " } } } }"})
		check := nativeFollowupCheck(t, root, "jest")
		require.Len(t, check.Jobs, 1)
		require.Equal(t, StatusExcluded, check.Jobs[0].Status)
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.Error(t, err, "an opted-out command is never an editable target")
	}
	root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { script { env.DD_CIVISIBILITY_ENABLED = params.ENABLED; sh 'npm test' } } } } }"})
	w, err := readWorkflows(root)
	require.NoError(t, err)
	require.NotEmpty(t, w[0].Jobs["pipeline"].Gaps)
	require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
}
