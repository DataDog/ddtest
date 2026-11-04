package onboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCypressRunStepOSMatrixKeepsEachRunnerPlatform(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix: {os: [ubuntu-latest, macos-latest, windows-latest]}
    steps:
      - run: npx cypress run
        shell: bash
`)
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	platforms := map[string]bool{}
	for _, entry := range scope.Commands {
		require.Nil(t, entry.Cypress, "a run: step keeps its CLI execution path")
		platforms[entry.Platform] = true
		require.Len(t, entry.Locations, 1)
	}
	require.Equal(t, map[string]bool{"linux": true, "darwin": true, "windows": true}, platforms)

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no hosted runner row matches this host")
	}
	// Default mode selects this host's row; the others stay unvalidated.
	selected, err := CypressExecutionCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, runtime.GOOS, selected.Platform)
}

func TestCypressActionSpecKeepsBraceGlobs(t *testing.T) {
	step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"spec": "cypress/e2e/{a,b}.cy.js\ncypress/e2e/c.cy.js\n"}}
	result := resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step)
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	// Like the action, newlines become commas and Cypress parses the braces.
	require.Equal(t, "cypress/e2e/{a,b}.cy.js,cypress/e2e/c.cy.js", result.Tests[0].Cypress.Options["spec"])
}

func TestCypressPrefixCommandRunsFromTheCallerDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{}`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "frontend"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "frontend", "package.json"), []byte(`{"devDependencies":{"cypress":"15.1.0"},"scripts":{"test":"cypress run"}}`), 0644))
	for _, command := range []string{"npm --prefix frontend test", "npm --prefix=frontend test"} {
		entry, err := CypressExecutionCommand(root, command)
		require.NoError(t, err, command)
		require.Equal(t, command, entry.Command)
		require.Equal(t, ".", entry.Directory, "the prefix is relative to the caller, not to frontend")
		require.Equal(t, "frontend", entry.CypressPackage)
	}
}

func TestGitHubRemotePathAcceptsSSHURLs(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/DataDog/ddtest.git":       "DataDog/ddtest.git",
		"git@github.com:DataDog/ddtest.git":           "DataDog/ddtest.git",
		"ssh://git@github.com/DataDog/ddtest.git":     "DataDog/ddtest.git",
		"ssh://git@github.com:22/DataDog/ddtest.git":  "DataDog/ddtest.git",
		"ssh://git@example.com/DataDog/ddtest.git":    "ssh://git@example.com/DataDog/ddtest.git",
		"https://gitlab.com/DataDog/ddtest.git":       "https://gitlab.com/DataDog/ddtest.git",
		"ssh://git@github.com.evil.test/DataDog/repo": "ssh://git@github.com.evil.test/DataDog/repo",
	} {
		require.Equal(t, want, githubRemotePath(remote), remote)
	}
}

func TestCypressCIContextsReadSSHOrigins(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "ssh://git@github.com/DataDog/ddtest.git"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	contexts := cypressCIContexts(root)
	require.NotEmpty(t, contexts, "fork and missing-secret contexts are still checked")
	require.Equal(t, "DataDog/ddtest", contexts[0]["github.repository"])
}

// cypress-example-kitchensink's GitLab jobs print the CI environment with
// @bahmutov/print-env, start the application in the background in one script
// entry and run Cypress in the next: validation starts the application the
// same way, and neither entry blocks.
func TestGitLabCypressBackgroundServerEntry(t *testing.T) {
	useCIProvider(t, "gitlab")
	job := func(script string) string {
		return ".job_template:\n  image: cypress/base:24.19.0\n  stage: test\n  script:\n" + script + "electrons-1:\n  extends: .job_template\n"
	}
	kitchensink := "    - npx print-env CI\n    - npm run start &\n    - npm run e2e:record -- --parallel --group \"electrons on GitLab CI\"\n"
	for _, tc := range []struct {
		name, script, unresolved string
	}{
		{"kitchensink", kitchensink, ""},
		{"check-only entry after the server", "    - npm run start &\n    - echo \"waiting for the server\"\n    - npm run e2e:record -- --parallel --group \"electrons on GitLab CI\"\n", ""},
		{"no test after the server", "    - npm run start &\n    - npm run lint\n    - npm run e2e:record\n", "shell control flow requires review"},
		{"server last", "    - npm run e2e:record\n    - npm run start &\n", "shell control flow requires review"},
		{"redirected server", "    - npm run start > server.log &\n    - npm run e2e:record\n", "shell control flow requires review"},
		{"unknown environment printer", "    - npx show-env CI\n    - npm run e2e:record\n", "show-env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"scripts":{"start":"node ./scripts/start.js","lint":"eslint","e2e:record":"cypress run --record"},"devDependencies":{"@bahmutov/print-env":"2.1.2","cypress":"15.21.1"}}`)
			writeFile(t, root, "scripts/start.js", "const { execSync } = require('child_process')\nexecSync(`serve --listen ${process.env.PORT || 8080}`, { stdio: 'inherit' })\n")
			writeFile(t, root, "cypress.config.js", "module.exports = {}\n")
			writeFile(t, root, ".gitlab-ci.yml", job(tc.script))
			scope, err := DiscoverValidationScope(root, "cypress")
			require.NoError(t, err)
			blockers := strings.Join(scope.Unresolved, "\n")
			for _, entry := range scope.Commands {
				blockers += strings.Join(entry.UnvalidatedSetup, "\n")
			}
			if tc.unresolved != "" {
				require.Contains(t, blockers, tc.unresolved)
				return
			}
			require.Empty(t, blockers)
			require.Len(t, scope.Commands, 1)
			entry := scope.Commands[0]
			require.Equal(t, `npm run e2e:record -- --parallel --group 'electrons on GitLab CI'`, entry.Command)
			require.NotNil(t, entry.Cypress)
			require.Equal(t, [][]string{{"npm", "run", "start"}}, entry.Cypress.Start)
			require.Equal(t, []string{"npm", "run", "e2e:record", "--", "--parallel", "--group", "electrons on GitLab CI"}, entry.Cypress.CLI)
			require.Contains(t, strings.Join(scope.Review, "\n"), "(npm run start &): Starts the application in the background for the next step's Cypress tests")
		})
	}
}

// The eval agent's cypress-example-kitchensink .gitlab-ci.yml after it added
// ddtest's GitLab setup entry by hand: the instructions put it immediately
// before the first test command, which is after npm run start &. The setup
// entry starts no test and leaves the server alone, so validation still
// starts the application before Cypress.
func TestGitLabCypressBackgroundServerAcrossDatadogSetup(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"start":"node ./scripts/start.js","e2e:record":"cypress run --record"},"devDependencies":{"@bahmutov/print-env":"2.1.2","cypress":"15.21.1"}}`)
	writeFile(t, root, "scripts/start.js", "const { execSync } = require('child_process')\nexecSync(`serve --listen ${process.env.PORT || 8080}`, { stdio: 'inherit' })\n")
	writeFile(t, root, "cypress.config.js", "module.exports = {}\n")
	writeFile(t, root, ".gitlab-ci.yml", `# all jobs that actually run tests can use the same definition
.job_template:
  image: cypress/base:24.19.0
  stage: test
  script:
    # print CI environment variables for reference
    - npx print-env CI
    # start the server in the background
    - npm run start &
    - |
      if [ -n "${DD_API_KEY:-}" ]; then
        LANGUAGES="js" SITE="datadoghq.com" API_KEY="$DD_API_KEY" JS_TRACER_VERSION="6.23.0" \
        SRC=$(curl -fsSL https://github.com/DataDog/test-optimization-gitlab-script/releases/download/v1.2.10/script.sh); \
        echo "$SRC" | sha256sum | grep -q '^c82030bdbc7cbbbb139751885ca0392a7191c858f5a080ae364e0ec16218b783' && \
          source <(echo "$SRC") || \
          echo "ERROR: SHA256 mismatch. Datadog Test Optimization autoinstrumentation not enabled." >&2
      else
        echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this job." >&2
      fi
    # run Cypress test in load balancing mode
    - npm run e2e:record -- --parallel --group "electrons on GitLab CI"
  artifacts:
    when: always
    paths:
      - cypress/screenshots/**/*.png
    expire_in: 1 day

electrons-1:
  extends: .job_template
electrons-2:
  extends: .job_template
electrons-3:
  extends: .job_template
electrons-4:
  extends: .job_template
electrons-5:
  extends: .job_template
`)
	requireCypressServerRuntimeCompatible(t, root, 5)
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1, "both jobs run the same command")
	for _, entry := range scope.Commands {
		require.Empty(t, entry.UnvalidatedSetup)
		require.NotNil(t, entry.Cypress)
		require.Equal(t, [][]string{{"npm", "run", "start"}}, entry.Cypress.Start)
	}
	require.Contains(t, strings.Join(scope.Review, "\n"), "electrons-1 / step 2 (npm run start &): Starts the application in the background")
}

// Every provider's instructions put the Datadog setup immediately before the
// first test step or command, which is after a step that starts the
// application in the background: the setup step doesn't separate the server
// from the Cypress test it starts for.
func TestCypressBackgroundServerAcrossProviderSetups(t *testing.T) {
	installer := func(dollar string) string {
		lines := []string{
			`if [ -n "` + dollar + `{DD_API_KEY:-}" ]; then`,
			`  export DD_SITE="datadoghq.com" DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="buildkite"`,
			`  if mkdir -p .datadog &&`,
			`    { curl -fsSL -o .datadog/install_test_visibility.sh https://install.datadoghq.com/scripts/install_test_visibility_v15.sh || wget -qO .datadog/install_test_visibility.sh https://install.datadoghq.com/scripts/install_test_visibility_v15.sh; } &&`,
			`    echo "e64a4a45e0b171f4294b9f949f7fc99946192f952a2d463c61fad3cb8b956a58  .datadog/install_test_visibility.sh" | { sha256sum -c - || shasum -a 256 -c -; } >/dev/null 2>&1 &&`,
			`    DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES="js" DD_SET_TRACER_VERSION_JS="6.23.0" bash .datadog/install_test_visibility.sh >.datadog/test-optimization.env; then`,
			`    while IFS='=' read -r dd_name dd_value; do`,
			`      case "` + dollar + `dd_name" in '' | [0-9]* | *[!A-Za-z0-9_]*) ;; *) export "` + dollar + `dd_name=` + dollar + `dd_value" ;; esac`,
			`    done <.datadog/test-optimization.env`,
			`  else`,
			`    echo "Datadog Test Optimization could not be installed; the tests run without it." >&2`,
			`  fi`,
			`else`,
			`  echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this step." >&2`,
			`fi`,
		}
		return strings.Join(lines, "\n")
	}
	indent := func(text, prefix string) string {
		return prefix + strings.ReplaceAll(text, "\n", "\n"+prefix)
	}
	for _, tc := range []struct{ provider, file, config string }{
		{"github", ".github/workflows/e2e.yml", `on: push
jobs:
  e2e:
    runs-on: ubuntu-latest
    env:
      DD_API_KEY: ${{ secrets.DD_API_KEY }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: {node-version: 24}
      - run: npm ci
      - run: npm run start &
      - name: Configure Datadog Test Optimization
        if: env.DD_API_KEY != ''
        uses: datadog/test-visibility-github-action@7ce545f228901e4ac4ef0b45d946120f1c4124e0 # v3.3.0
        with:
          languages: js
          api_key: ${{ secrets.DD_API_KEY }}
          site: datadoghq.com
          js-tracer-version: 6.23.0
      - run: npm run e2e
        env:
          NODE_OPTIONS: ${{ env.DD_API_KEY != '' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}
`},
		{"circleci", ".circleci/config.yml", `version: 2.1
orbs:
  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2
jobs:
  e2e:
    docker:
      - image: cimg/node:24.11.0-browsers
    steps:
      - checkout
      - run: npm ci
      - run: npm run start &
      - test-optimization-circleci-orb/autoinstrument:
          languages: js
          site: datadoghq.com
          js_tracer_version: 6.23.0
      - run: npm run e2e
workflows:
  main:
    jobs: [e2e]
`},
		{"buildkite", ".buildkite/pipeline.yml", "steps:\n  - label: e2e\n    commands:\n      - npm ci\n      - npm run start &\n      - |\n" + indent(installer("$$"), "        ") + "\n      - npm run e2e\n"},
		{"jenkins", "Jenkinsfile", "pipeline {\n  agent { docker { image 'cimg/node:24.11.0-browsers' } }\n  stages {\n    stage('e2e') {\n      steps {\n        sh 'npm ci'\n        sh '''\nnpm run start &\n" +
			jenkinsContainerSnippet +
			"\nnpm run e2e\n'''\n      }\n    }\n  }\n}\n"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			useCIProvider(t, tc.provider)
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"scripts":{"start":"node ./scripts/start.js","e2e":"cypress run"},"devDependencies":{"cypress":"15.21.1"}}`)
			// kitchensink's start script: a shell command ddtest can't read
			// could start tests, unless the step is paired as the server.
			writeFile(t, root, "scripts/start.js", "const { execSync } = require('child_process')\nexecSync(`serve --listen ${process.env.PORT || 8080}`, { stdio: 'inherit' })\n")
			writeFile(t, root, "cypress.config.js", "module.exports = {}\n")
			writeFile(t, root, tc.file, tc.config)
			if tc.provider == "circleci" {
				// The others' fixtures leave the runtime check inconclusive
				// for reasons unrelated to the server (an unreadable setup
				// condition, an unknown host runtime, an unbound API key).
				requireCypressServerRuntimeCompatible(t, root, 1)
			}
			scope, err := DiscoverValidationScope(root, "cypress")
			require.NoError(t, err)
			require.NotContains(t, strings.Join(scope.Unresolved, "\n"), "npm run start &")
			require.Len(t, scope.Commands, 1)
			require.NotNil(t, scope.Commands[0].Cypress, "the test starts the server first")
			require.Equal(t, [][]string{{"npm", "run", "start"}}, scope.Commands[0].Cypress.Start)
			require.Contains(t, strings.Join(scope.Review, "\n"), "Starts the application in the background")
		})
	}
}

// requireCypressServerRuntimeCompatible checks that the CI runtime check,
// which the CI tracer agreement reads, treats a background server step the
// scope review pairs with the Cypress test as neither a test nor an
// unresolved command: each of the jobs has one compatible Cypress step.
func requireCypressServerRuntimeCompatible(t *testing.T, root string, jobs int) {
	t.Helper()
	check := checkCIRuntimes(t.Context(), root, func(context.Context, string, string) (NodeResolution, error) {
		return NodeResolution{Version: "24.11.0", Source: "test"}, nil
	}, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.23.0", Node: ">=18"}, nil
	}, "cypress")
	require.Equal(t, StatusCompatible, check.Status, "%+v", check)
	require.Len(t, check.Jobs, jobs, "%+v", check.Jobs)
	for _, job := range check.Jobs {
		require.Equal(t, StatusCompatible, job.Status, "%+v", job)
		require.NotContains(t, job.Command, "npm run start &", "%+v", job)
	}
	for _, finding := range check.Review {
		require.NotContains(t, finding.Command, "npm run start &", "%+v", finding)
	}
}

// jenkinsContainerSnippet is ddtest's Jenkins container setup for dd-trace
// 6.23.0, as onboard --instructions prints it.
const jenkinsContainerSnippet = `if [ "${DD_API_KEY:+set}" = set ]; then
  export DD_TRACER_FOLDER="$(mktemp -d "$PWD/.datadog-ci-container.XXXXXX")" DD_SITE="datadoghq.com" DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="jenkins"
  if mkdir -p "$DD_TRACER_FOLDER" &&
    { curl -fsSL -o "$DD_TRACER_FOLDER/install_test_visibility.sh" https://install.datadoghq.com/scripts/install_test_visibility_v15.sh || wget -qO "$DD_TRACER_FOLDER/install_test_visibility.sh" https://install.datadoghq.com/scripts/install_test_visibility_v15.sh; } &&
    echo "e64a4a45e0b171f4294b9f949f7fc99946192f952a2d463c61fad3cb8b956a58  $DD_TRACER_FOLDER/install_test_visibility.sh" | { sha256sum -c - || shasum -a 256 -c -; } >/dev/null 2>&1 &&
    DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES="js" DD_SET_TRACER_VERSION_JS="6.23.0" bash "$DD_TRACER_FOLDER/install_test_visibility.sh" >"$DD_TRACER_FOLDER/test-optimization.env"; then
    while IFS='=' read -r dd_name dd_value; do
      case "$dd_name" in '' | [0-9]* | *[!A-Za-z0-9_]*) ;; *) export "$dd_name=$dd_value" ;; esac
    done <"$DD_TRACER_FOLDER/test-optimization.env"
  else
    echo "Datadog Test Optimization could not be installed; the tests run without it." >&2
  fi
else
  echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this step." >&2
fi`

// Validation starts a paired background server with the Cypress test's
// environment and directory, so a server step that has its own (a step env
// such as PORT, another working directory) stays unpaired: unresolved, as
// any shell control flow ddtest can't replay.
func TestCypressBackgroundServerKeepsItsContext(t *testing.T) {
	workflow := func(server string) string {
		return "on: push\njobs:\n  e2e:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-node@v4\n        with: {node-version: 24}\n      - run: npm ci\n" + server + "      - run: npm run e2e\n"
	}
	for _, tc := range []struct {
		name, provider, file, config string
		paired                       bool
	}{
		{"same context", "github", ".github/workflows/e2e.yml", workflow("      - run: npm run start &\n"), true},
		{"same step env", "github", ".github/workflows/e2e.yml", strings.Replace(workflow("      - run: npm run start &\n        env:\n          PORT: \"3001\"\n"), "      - run: npm run e2e\n", "      - run: npm run e2e\n        env:\n          PORT: \"3001\"\n", 1), true},
		{"own step env", "github", ".github/workflows/e2e.yml", workflow("      - run: npm run start &\n        env:\n          PORT: \"3001\"\n"), false},
		{"own working directory", "github", ".github/workflows/e2e.yml", workflow("      - run: npm run start &\n        working-directory: server\n"), false},
		{"own inline variable", "gitlab", ".gitlab-ci.yml", "e2e:\n  image: cypress/base:24.19.0\n  script:\n    - npm ci\n    - PORT=3001 npm run start &\n    - npm run e2e\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useCIProvider(t, tc.provider)
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"scripts":{"start":"node ./scripts/start.js","e2e":"cypress run"},"devDependencies":{"cypress":"15.21.1"}}`)
			writeFile(t, root, "server/package.json", `{"scripts":{"start":"node ./scripts/start.js"}}`)
			writeFile(t, root, "scripts/start.js", "const { execSync } = require('child_process')\nexecSync(`serve --listen ${process.env.PORT || 8080}`, { stdio: 'inherit' })\n")
			writeFile(t, root, "cypress.config.js", "module.exports = {}\n")
			writeFile(t, root, tc.file, tc.config)
			scope, err := DiscoverValidationScope(root, "cypress")
			require.NoError(t, err)
			if !tc.paired {
				require.Contains(t, strings.Join(scope.Unresolved, "\n"), "npm run start &")
				require.NotContains(t, strings.Join(scope.Review, "\n"), "Starts the application in the background")
				return
			}
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.NotNil(t, scope.Commands[0].Cypress)
			require.Equal(t, [][]string{{"npm", "run", "start"}}, scope.Commands[0].Cypress.Start)
		})
	}
}
