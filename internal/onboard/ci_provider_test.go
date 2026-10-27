// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func providerFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if files["package.json"] == "" {
		files["package.json"] = `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"30.5.1"}}`
	}
	for path, contents := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(contents), 0600))
	}
	return root
}

func selectProviderForTest(t *testing.T, provider string) {
	t.Helper()
	config := settings.Get()
	before := config.CIProvider
	config.CIProvider = provider
	t.Cleanup(func() { config.CIProvider = before })
}

func TestProviderAdaptersShareDiscoveryAndSourceEditor(t *testing.T) {
	for _, fixture := range []struct{ provider, path, contents string }{
		{"gitlab", ".gitlab-ci.yml", "default:\n  image: node:24.1.0\n  before_script: [npm ci]\ntest:\n  script: npm test # keep this comment\n"},
		{"circleci", ".circleci/config.yml", "version: 2.1\njobs:\n  test:\n    docker: [{image: 'cimg/node:24.1.0'}]\n    steps:\n      - checkout\n      - run: npm ci\n      - run: |\n          npm test\nworkflows:\n  main:\n    jobs: [test]\n"},
		{"buildkite", ".buildkite/pipeline.yml", "steps:\n  - label: tests\n    plugins:\n      - docker#v5.13.0: {image: 'node:24.1.0'}\n    command: npm test\n"},
		{"jenkins", "jenkins/Jenkinsfile", "pipeline { agent any; tools { nodejs '24.1.0' }; environment { NODE_ENV = 'test' }; stages { stage('Build') { steps { sh 'npm ci' } }; stage('Test') { steps { sh 'npm test' } } } }\n"},
	} {
		t.Run(fixture.provider, func(t *testing.T) {
			selectProviderForTest(t, fixture.provider)
			root := providerFixture(t, map[string]string{fixture.path: fixture.contents})
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Len(t, scope.Commands, 1)
			require.Empty(t, scope.Unresolved)
			require.Equal(t, fixture.provider, scope.CIProvider)
			require.Equal(t, fixture.provider, scope.Commands[0].CIProvider)
			require.NotContains(t, scope.Commands[0].Environment, "GITHUB_ACTIONS")
			before, err := os.ReadFile(filepath.Join(root, fixture.path))
			require.NoError(t, err)
			plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.eu", ">=22")
			require.NoError(t, err)
			diff, err := plan.Diff()
			require.NoError(t, err)
			require.Contains(t, diff, "dd-trace@6.20.1")
			require.NotContains(t, diff, githubAction)
			unchanged, err := os.ReadFile(filepath.Join(root, fixture.path))
			require.NoError(t, err)
			require.Equal(t, before, unchanged, "preview writes nothing")
			_, err = plan.Apply(true)
			require.NoError(t, err)
			updated, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, updated.Unresolved)
			require.Len(t, updated.Commands, 1)
			require.Equal(t, scope.Commands[0].Command, updated.Commands[0].Command)
			check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
			}, "jest")
			require.Equal(t, StatusCompatible, check.Status, "%+v", check)
			second, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.eu", ">=22")
			require.NoError(t, err)
			require.True(t, second.Configured(), "repeated setup must be idempotent")
			if strings.Contains(fixture.contents, "# keep") {
				contents, err := os.ReadFile(filepath.Join(root, fixture.path))
				require.NoError(t, err)
				require.Contains(t, string(contents), "# keep this comment")
			}
		})
	}
}

func TestProviderSelectionRequiresHumanChoiceOnlyForTestConsumers(t *testing.T) {
	selectProviderForTest(t, "")
	root := providerFixture(t, map[string]string{
		".github/workflows/test.yml": "jobs:\n  tests:\n    steps: [{run: npm test}]\n",
		".gitlab-ci.yml":             "test:\n  script: npm test\n",
	})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.Equal(t, []string{"github", "gitlab"}, choice.Providers)
	require.Empty(t, ProviderChoiceResult(choice).NextCommand)
	t.Chdir(root)
	var output bytes.Buffer
	require.Error(t, StartWithOptions(&output, StartOptions{Format: "json"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "ci_provider_required", result.Code)
	require.Empty(t, result.NextCommand)
	selected, err := SelectCIProvider(root, "gitlab")
	require.NoError(t, err)
	require.Equal(t, "gitlab", selected)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitlab-ci.yml"), []byte("lint:\n  script: echo lint\n"), 0600))
	selected, err = SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "github", selected, "a provider containing only non-test jobs must not force a choice")
}

func TestGitLabInheritanceIncludesAndMatrixRemainVisible(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{
		".gitlab-ci.yml": "include:\n - local: ci/base.yml\n - project: external/templates\n   file: test.yml\ntest:\n  extends: .base\n  parallel:\n    matrix:\n      - NODE_VERSION: ['22.1.0','24.1.0']\n  script: npm test\n",
		"ci/base.yml":    ".base:\n  image: node:$NODE_VERSION\n  variables: {NODE_ENV: test}\n  before_script: [npm ci]\n",
	})
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	require.Len(t, workflows[0].Jobs, 2)
	for _, job := range workflows[0].Jobs {
		require.NotEmpty(t, job.Node)
		require.Equal(t, "test", job.Env["NODE_ENV"])
	}
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Commands)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "external/conditional")
}

func TestAdaptersNeverExecuteConfigurationAndRejectEscapingIncludes(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "include: {local: '../outside.yml'}\ntest: {script: npm test}\n"})
	w, err := readWorkflows(root)
	require.NoError(t, err)
	require.Contains(t, strings.Join(w[0].Unresolved, "\n"), "include")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitlab-ci.yml"), []byte("test: &loop {script: *loop}\n"), 0600))
	_, err = readWorkflows(root)
	require.ErrorContains(t, err, "cyclic")
}

func TestJenkinsIgnoresCommentsAndReportsDynamicCode(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	root := providerFixture(t, map[string]string{"Jenkinsfile": "// sh 'npm test'\n/* sh 'npm test' */\nnode { stage('Test') { if (params.RUN_TESTS) { sh 'npm test' } } }\n"})
	w, err := readWorkflows(root)
	require.NoError(t, err)
	require.Len(t, w[0].Jobs["pipeline"].Steps, 1)
	require.NotEmpty(t, w[0].Jobs["pipeline"].Gaps)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup)
}

func TestNativeInitializationRunsOriginalCommandWithoutCredentials(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	root := t.TempDir()
	command := exec.Command("sh", "-c", wrapNativeCommand(nativeSetup{Framework: "jest", Version: "6.20.1", Site: "datadoghq.com"}, "printf original", "gitlab"))
	command.Dir = root
	command.Env = append(os.Environ(), "DD_API_KEY=")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "original", string(output))
	require.NoDirExists(t, filepath.Join(root, ".testoptimization"))
}

func TestNativeInitializationPreservesExitStatusAndCleansPrivateInstall(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	for _, install := range []string{"0", "1"} {
		t.Run(install, func(t *testing.T) {
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			cache := filepath.Join(root, "cache with spaces")
			require.NoError(t, os.MkdirAll(tools, 0700))
			require.NoError(t, os.MkdirAll(cache, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(tools, "npm"), []byte("#!/bin/sh\nexit "+install+"\n"), 0700))
			original := `printf '%s\n' "$NODE_OPTIONS"; exit 7`
			cmd := exec.Command("sh", "-c", wrapNativeCommand(nativeSetup{Framework: "jest", Version: "6.20.1", Site: "datadoghq.com", ESM: true}, original, "gitlab"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+cache, "DD_API_KEY=synthetic", "NODE_OPTIONS=--conditions=customer")
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.Equal(t, 7, exit.ExitCode(), string(output))
			require.Contains(t, string(output), "--conditions=customer")
			if install == "0" {
				require.Contains(t, string(output), `-r "`)
				require.Contains(t, string(output), `--import "`)
			} else {
				require.NotContains(t, string(output), "ci/init")
			}
			entries, err := os.ReadDir(cache)
			require.NoError(t, err)
			require.Empty(t, entries, "CI tracer installation is removed even when tests fail")
			require.NoDirExists(t, filepath.Join(root, "node_modules"))
			require.NoFileExists(t, filepath.Join(root, "package-lock.json"))
		})
	}
}

func TestNativeShellPersistenceAndProviderIsolation(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n  image: node:24.1.0\n  before_script:\n    - export NODE_ENV=production\n    - cd package\n  script: npm test\n", "package/package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1"}}`})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, "package", scope.Commands[0].Directory)
	require.Equal(t, "production", scope.Commands[0].Environment["NODE_ENV"])
	require.Equal(t, "true", WithCIDefaults(scope.Commands[0].Environment, scope.CIProvider)["GITLAB_CI"])
	require.NotContains(t, WithCIDefaults(scope.Commands[0].Environment, scope.CIProvider), "GITHUB_ACTIONS")
}

func TestNativeReaderKeepsUnknownPluginsAndOrbBindingsUnresolved(t *testing.T) {
	root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": "steps:\n - command: npm test\n   plugins: ['$NVM_PLUGIN']\n"})
	workflow, err := readBuildkite(root, ".buildkite/pipeline.yml")
	require.NoError(t, err)
	for _, job := range workflow.Jobs {
		require.Contains(t, strings.Join(job.Gaps, "\n"), "$NVM_PLUGIN")
	}
	root = providerFixture(t, map[string]string{".circleci/config.yml": "orbs: {node: 'someone/custom@1.2.3'}\njobs:\n test:\n  steps:\n   - node/install-packages: {pkg-manager: npm}\n   - run: npm test\n"})
	workflow, err = readCircleCI(root, ".circleci/config.yml")
	require.NoError(t, err)
	require.Contains(t, strings.Join(workflow.Unresolved, "\n"), "not bound to an exact official")
}

func TestNativeEnvironmentAndScriptRecursionStayBounded(t *testing.T) {
	value, err := nativeEnvironmentValue("$BASE/fixtures", map[string]string{"BASE": "src"})
	require.NoError(t, err)
	require.Equal(t, "src/fixtures", value)
	for _, value := range []string{"$MISSING", "$(touch marker)", "${A}"} {
		_, err := nativeEnvironmentValue(value, map[string]string{"A": "$A"})
		require.Error(t, err)
	}
	root := providerFixture(t, map[string]string{"loop.sh": "#!/bin/sh\n./loop.sh\n"})
	require.NoError(t, os.Chmod(filepath.Join(root, "loop.sh"), 0700))
	remaining := 256
	r, ok := resolveRepositoryShell(root, root, []string{"./loop.sh"}, nil, &remaining, "jest")
	require.True(t, ok)
	require.NotEmpty(t, r.Reason)
	require.False(t, r.Matched)
	require.NoFileExists(t, filepath.Join(root, "marker"))
}

func TestProviderChoiceIsRetainedInAgentCommands(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test: {script: npm test}\n"})
	t.Chdir(root)
	var out bytes.Buffer
	require.NoError(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.Contains(t, result.NextCommand, "--ci-provider")
	require.Contains(t, result.NextCommand, "gitlab")
}

func TestUnsupportedCIRequestsSpecificInputWithoutProposingGitHubEdits(t *testing.T) {
	selectProviderForTest(t, "")
	root := providerFixture(t, map[string]string{"azure-pipelines.yml": "steps:\n - script: npm test\n"})
	t.Chdir(root)
	var out bytes.Buffer
	require.NoError(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.Equal(t, "ci_provider_unrecognized", result.Code)
	require.NotEmpty(t, result.RequiredInput)
	require.Empty(t, result.NextCommand)
}

func TestNativeExistingPackageInitializationIsPreserved(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{
		"package.json":   `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
		".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init --import dd-trace/register.js'\n script: npm test\n",
	})
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	step := workflows[0].Jobs["test"].Steps[2]
	require.True(t, step.Native.Manual)
	require.Equal(t, "6.20.1", step.Native.Tracer)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, check.Status, "%+v", check)
	plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	diff, err := plan.Diff()
	require.NoError(t, err)
	require.NotContains(t, diff, ".gitlab-ci.yml", "existing bootstrap must not be replaced or duplicated")
}

func TestNativeBootstrapLoadsBothHooksWithSpacesInPaths(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	root := t.TempDir()
	tools := filepath.Join(root, "tools")
	cache := filepath.Join(root, "cache with spaces")
	require.NoError(t, os.MkdirAll(tools, 0700))
	require.NoError(t, os.MkdirAll(cache, 0700))
	installer := `#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--prefix" ]; then shift; prefix=$1; fi
 shift
done
mkdir -p "$prefix/node_modules/dd-trace/ci"
printf 'global.__ciRequire = true;' > "$prefix/node_modules/dd-trace/ci/init.js"
printf 'global.__ciImport = true;' > "$prefix/node_modules/dd-trace/register.js"
`
	require.NoError(t, os.WriteFile(filepath.Join(tools, "npm"), []byte(installer), 0700))
	original := `"` + node + `" -e 'console.log(global.__ciRequire, global.__ciImport)'`
	cmd := exec.Command("sh", "-c", wrapNativeCommand(nativeSetup{Framework: "jest", Version: "6.20.1", Site: "datadoghq.com", ESM: true}, original, "gitlab"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+cache, "DD_API_KEY=synthetic", "NODE_OPTIONS=")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "true true", strings.TrimSpace(string(output)))
	entries, err := os.ReadDir(cache)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCIImageVariablesMatchWholeNames(t *testing.T) {
	require.Empty(t, ciImageNode("node:$NODE_VERSION", map[string]string{"NODE": "24"}), "a prefix variable must not resolve a different variable")
	require.Equal(t, "24.1.0", ciImageNode("node:$NODE_VERSION", map[string]string{"NODE": "18", "NODE_VERSION": "24.1.0"}))
}
