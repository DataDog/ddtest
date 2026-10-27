// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeStateJenkinsCallBoundary(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, separator := range []string{"; ", "\n"} {
		root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh testCommand" + separator + "sh script: 'npm test' } } } }"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		require.Contains(t, strings.Join(w[0].Unresolved, ";"), "computed")
		require.Len(t, w[0].Jobs["pipeline"].Steps, 1)
		_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "computed")
	}
	root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh returnStatus: true,\n script: 'npm test' } } } }"})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err, "named arguments may continue after a comma")
}

func TestNativeStateUnresolvedJobProviderChoice(t *testing.T) {
	root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { stages { stage('Test') { steps { sh 'npm ci'; bat 'npm test' } } } }", ".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n"})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.ElementsMatch(t, []string{"github", "jenkins"}, choice.Providers)
}

func TestNativeStateComposeEnvironment(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	for _, value := range []string{"DD_CIVISIBILITY_ENABLED=false", "NODE_ENV=production"} {
		root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": "steps:\n - command: npm test\n   plugins:\n    - docker-compose#v5.14.0:\n       run: app\n       env: ['" + value + "']\n", "docker-compose.yml": "services:\n app: {image: 'node:24.1.0'}\n"})
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "environment")
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}

func TestNativeStateUnsetPreload(t *testing.T) {
	for _, provider := range []string{"gitlab", "buildkite"} {
		t.Run(provider, func(t *testing.T) {
			selectProviderForTest(t, provider)
			path, text := ".gitlab-ci.yml", "test:\n image: node:24.1.0\n before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n  - unset NODE_OPTIONS\n script: npm test\n"
			if provider == "buildkite" {
				path, text = ".buildkite/pipeline.yml", "image: node:24.1.0\nsteps:\n - commands:\n    - npm ci\n    - export NODE_OPTIONS='-r dd-trace/ci/init'\n    - unset NODE_OPTIONS\n    - npm test\n"
			}
			root := providerFixture(t, map[string]string{path: text, "package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`})
			require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "unset")
		})
	}
}

func TestNativeStateExistingScopedInitialization(t *testing.T) {
	for _, framework := range []string{"cucumber", "webdriverio"} {
		t.Run(framework, func(t *testing.T) {
			selectProviderForTest(t, "gitlab")
			key, script := "DD_CUCUMBER_NODE_OPTIONS", cucumberScopedOptions+" nyc -- cucumber-js"
			dependencies := map[string]string{"dd-trace": "6.20.1", "@cucumber/cucumber": "13.2.1", "nyc": "17.1.0"}
			if framework == "webdriverio" {
				key, script = "DD_WDIO_NODE_OPTIONS", webdriverIOScopedOptions+" wdio run wdio.conf.js"
				dependencies = map[string]string{"dd-trace": "6.20.1", "@wdio/cli": "9.20.0"}
			}
			manifest, err := json.Marshal(map[string]any{"scripts": map[string]string{"test": script}, "devDependencies": dependencies})
			require.NoError(t, err)
			root := providerFixture(t, map[string]string{"package.json": string(manifest), "wdio.conf.js": "exports.config = { runner: 'local', framework: 'mocha' }\n", ".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - npm ci\n  - export " + key + "='-r dd-trace/ci/init --import dd-trace/register.js'\n script: npm test\n"})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			native := w[0].Jobs["test"].Steps[2].Native
			require.Equal(t, "6.20.1", native.Tracer)
			require.Equal(t, framework, native.Framework)
			require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, framework).Status)
			_, err = PlanJavaScriptSetup(root, framework, "6.21.0", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "differs")
			plan, err := PlanJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
			diff, err := plan.Diff()
			require.NoError(t, err)
			require.NotContains(t, diff, ".gitlab-ci.yml")
			unscoped := strings.TrimPrefix(strings.TrimPrefix(script, cucumberScopedOptions+" "), webdriverIOScopedOptions+" ")
			withoutHook, err := json.Marshal(map[string]any{"scripts": map[string]string{"test": unscoped}, "devDependencies": dependencies})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), withoutHook, 0600))
			require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, framework).Status, "manual scoped initialization still requires consumption by the selected leaf")
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(strings.Replace(string(manifest), `"6.20.1"`, `"^6.20.1"`, 1)), 0600))
			_, err = PlanJavaScriptSetup(root, framework, "6.21.0", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "custom tracer")
		})
	}
}

func TestNativeStateBuildkiteUploadEscapes(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": "image: node:24.1.0\nsteps:\n - env: {TEST_SCRIPT: test}\n   command: npm run $$TEST_SCRIPT\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	actual, err := os.ReadFile(filepath.Join(root, ".buildkite/pipeline.yml"))
	require.NoError(t, err)
	require.Contains(t, string(actual), "$$TEST_SCRIPT")
	require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
}

func TestNativeStateCircleCIReusableCheckout(t *testing.T) {
	selectProviderForTest(t, "circleci")
	root := providerFixture(t, map[string]string{".circleci/config.yml": "commands:\n checkout-repo:\n  steps: [checkout]\n setup:\n  steps: [checkout-repo]\njobs:\n test:\n  working_directory: ~/project\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps:\n   - setup\n   - run: npm test\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, ".", scope.Commands[0].Directory)
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
}
