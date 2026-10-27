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

func TestNativeContractVariableTargetsAndInlineOptOut(t *testing.T) {
	for _, provider := range []string{"gitlab", "buildkite"} {
		for _, framework := range []string{"jest", "mocha"} {
			t.Run(provider+"/"+framework, func(t *testing.T) {
				selectProviderForTest(t, provider)
				path, source := ".gitlab-ci.yml", "test:\n image: node:24.1.0\n variables: {TEST_SCRIPT: test}\n script: npm run $TEST_SCRIPT\ndisabled:\n image: node:24.1.0\n script: DD_CIVISIBILITY_ENABLED=false npm test\n"
				if provider == "buildkite" {
					path, source = ".buildkite/pipeline.yml", "image: node:24.1.0\nsteps:\n - key: test\n   env: {TEST_SCRIPT: test}\n   command: npm run $$TEST_SCRIPT\n - key: disabled\n   command: DD_CIVISIBILITY_ENABLED=false npm test\n"
				}
				files := map[string]string{path: source}
				if framework == "mocha" {
					files["package.json"] = `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0"}}`
				}
				root := providerFixture(t, files)
				plan, err := PlanJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", ">=22")
				require.NoError(t, err)
				_, err = plan.Apply(true)
				require.NoError(t, err)
				w, err := readWorkflows(root)
				require.NoError(t, err)
				require.Equal(t, "6.20.1", w[0].Jobs["test"].Steps[0].Native.Tracer)
				reference := "$TEST_SCRIPT"
				if provider == "buildkite" {
					reference = "$$TEST_SCRIPT"
				}
				require.Contains(t, w[0].Jobs["test"].Steps[0].Native.Original, "npm run "+reference)
				require.Empty(t, w[0].Jobs["disabled"].Steps[0].Native.Tracer)
				check := nativeFollowupCheck(t, root, framework)
				require.Len(t, check.Jobs, 2, "the disabled consumer must remain visible")
				seenDisabled := false
				for _, f := range check.Jobs {
					if f.Job == "disabled" {
						seenDisabled = true
						require.Equal(t, StatusExcluded, f.Status)
					} else {
						require.Equal(t, StatusCompatible, f.Status, "%+v", f)
					}
				}
				require.True(t, seenDisabled)
				second, err := PlanJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", ">=22")
				require.NoError(t, err)
				require.True(t, second.Configured())
			})
		}
	}
}
func TestNativeContractGitLabNullVariables(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, parents := range []string{"extends: .base\n variables: null", "extends: .clear", "extends: [.base, .clear]"} {
		t.Run(parents, func(t *testing.T) {
			root := providerFixture(t, map[string]string{".gitlab-ci.yml": ".base:\n variables: {DD_CIVISIBILITY_ENABLED: 'false', PARENT: inherited}\n.clear:\n extends: .base\n variables: null\ntest:\n " + parents + "\n image: node:24.1.0\n script: npm test\n"})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			require.NotContains(t, w[0].Jobs["test"].Env, "DD_CIVISIBILITY_ENABLED")
			require.NotContains(t, w[0].Jobs["test"].Env, "PARENT")
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
		})
	}
}

func TestNativeContractGitLabExpansionPolicyIsExplicit(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, source := range []string{
		"test:\n image: node:24.1.0\n variables: {TOKEN: blue, PATTERN: {value: '$TOKEN', expand: false}}\n script: npm test\n",
		"variables: {TOKEN: blue, PATTERN: {value: '$TOKEN', expand: false}}\ntest:\n image: node:24.1.0\n script: npm test\n",
	} {
		root := providerFixture(t, map[string]string{".gitlab-ci.yml": source})
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Contains(t, strings.Join(scope.Unresolved, ";"), "expand: false")
		_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "expand: false")
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}

func TestNativeContractJenkinsCompleteArguments(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, call := range []string{`dir('packages/unit' + params.SUFFIX)`, `withEnv(["MODE=${params.MODE}"])`, `withEnv(['MODE=' + params.MODE])`, `withEnv(bindings)`} {
		t.Run(call, func(t *testing.T) {
			text := "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { " + call + " { sh 'npm test' } } } } }"
			root := providerFixture(t, map[string]string{"Jenkinsfile": text, "packages/unit/package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1"}}`})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			require.NotEmpty(t, w[0].Jobs["pipeline"].Gaps)
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.Error(t, err)
			actual, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
			require.NoError(t, err)
			require.Equal(t, text, string(actual))
		})
	}
	root := providerFixture(t, map[string]string{"Jenkinsfile": `pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { withEnv(['MODE=test', 'PATTERN=$TOKEN']) { sh 'npm test' } } } } }`})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "test", scope.Commands[0].Environment["MODE"])
	require.Equal(t, "$TOKEN", scope.Commands[0].Environment["PATTERN"])
}

func TestNativeContractCircleCIInvocationSteps(t *testing.T) {
	selectProviderForTest(t, "")
	root := providerFixture(t, map[string]string{
		".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n",
		".circleci/config.yml":       "jobs:\n build:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps:\n   - run: echo middle\nworkflows:\n main:\n  jobs:\n   - build:\n      pre-steps:\n       - run: npm test\n      post-steps:\n       - run: npm test\n",
	})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.Equal(t, []string{"github", "circleci"}, choice.Providers)
	w, err := readSelectedWorkflows(root, "circleci")
	require.NoError(t, err)
	for _, job := range w[0].Jobs {
		require.Len(t, job.Steps, 3)
		require.Equal(t, "npm test", job.Steps[0].Run)
		require.Equal(t, "echo middle", job.Steps[1].Run)
		require.Equal(t, "npm test", job.Steps[2].Run)
	}
}

func TestNativeContractCircleCIOrbInstallOverrides(t *testing.T) {
	selectProviderForTest(t, "circleci")
	for _, option := range []string{"override-ci-command: npm ci --omit=dev", "app-dir: tools"} {
		root := providerFixture(t, map[string]string{
			"package.json":         `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
			".circleci/config.yml": "orbs: {node: 'circleci/node@7.2.1'}\njobs:\n test:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps:\n   - node/install-packages: {pkg-manager: npm, " + option + "}\n   - run:\n      command: npm test\n      environment: {NODE_OPTIONS: '-r dd-trace/ci/init'}\n",
		})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		for _, job := range w[0].Jobs {
			require.NotEmpty(t, job.Gaps)
			require.Empty(t, job.Steps[len(job.Steps)-1].Native.Tracer)
		}
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}

func TestNativeContractDockerfileRuntimeMutation(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	for _, instruction := range []string{"RUN npm install --global n && n 16.20.2", "RUN npm install --global n && \\\n n 16.20.2", "RUN npm ci"} {
		t.Run(instruction, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				".buildkite/pipeline.yml": "steps:\n - key: test\n   command: npm test\n   plugins:\n    - docker-compose#v5.12.0: {run: app}\n",
				"docker-compose.yml":      "services:\n app:\n  build: .\n",
				"Dockerfile":              "FROM node:24.1.0\n" + instruction + "\n",
			})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			job := w[0].Jobs["test"]
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			if instruction == "RUN npm ci" {
				require.Equal(t, "24.1.0", job.Node)
				require.NoError(t, err)
			} else {
				require.Empty(t, job.Node)
				require.NotEmpty(t, job.Gaps)
				require.Error(t, err)
			}
		})
	}
}

func TestNativeContractCucumberHookConsumption(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	manifest := `{"scripts":{"test":"nyc -- cucumber-js"},"devDependencies":{"@cucumber/cucumber":"13.2.1","nyc":"17.1.0"}}`
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:24.1.0\n script: npm test\n", "package.json": manifest})
	plan, err := PlanJavaScriptSetup(root, "cucumber", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "cucumber").Status)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0600))
	check := nativeFollowupCheck(t, root, "cucumber")
	require.NotEqual(t, StatusCompatible, check.Status)
	require.Equal(t, CodeUnverifiedInitialization, check.Jobs[0].Code)
}

func TestNativeContractInstallDestinations(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, flag := range []string{"--prefix=tools", "--prefix tools", "--global", "--global=true", "-g", "--global=false"} {
		t.Run(flag, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				"package.json":   `{"scripts":{"test":"jest"},"dependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
				".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - npm install " + flag + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n",
			})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			tracer := w[0].Jobs["test"].Steps[2].Native.Tracer
			if flag == "--global=false" {
				require.Equal(t, "6.20.1", tracer)
			} else {
				require.Empty(t, tracer)
			}
		})
	}
}
