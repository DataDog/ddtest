// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Modeled on expressjs/express: npm configuration and pwsh output steps before
// the tests, and coverage-merge and publish jobs that never run Mocha.
func TestDiscoveryIgnoresJobsAndSetupOutsideTheTestPath(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix: {os: [ubuntu-latest, windows-latest], node-version: [18, 24]}
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '${{ matrix.node-version }}'}
      - run: |
          npm config set loglevel error
        shell: bash
      - run: npm install
      - run: |
          echo "Node.js version: $(node -v)"
          echo "NPM version: $(npm -v)"
      - run: npm run test-ci
        shell: bash
  coverage:
    needs: test
    runs-on: ubuntu-latest
    steps:
      - run: sudo apt-get -y install lcov
      - run: find ./coverage -name lcov.info -exec printf '-a %q\n' {} \; | xargs lcov -o ./lcov.info
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: npm install -g npm@latest
      - run: |
          cat > release-notes.md <<'EOF'
          Run npm test before publishing.
          EOF
      - run: npm stage publish --tag "$NPM_DIST_TAG"
        env: {NPM_DIST_TAG: '${{ steps.dist-tag.outputs.tag }}'}
`)
	writeScripts(t, root, map[string]string{"test-ci": "mocha test/"})
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Commands[0].UnvalidatedSetup)
	require.Len(t, scope.Commands[0].Locations, 4, "every matrix row, including the Windows pwsh rows")
	review := strings.Join(scope.Review, "\n")
	require.Contains(t, review, "Not a mocha test consumer: .github/workflows/test.yml / coverage / step 1")
	require.Contains(t, review, "Not a mocha test consumer: .github/workflows/test.yml / publish")
	require.Contains(t, review, "npm configuration setup is outside mocha validation")

	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
	}, "mocha")
	for _, job := range check.Jobs {
		require.Equal(t, "test", job.Job, "jobs that can't start Mocha are not checked as instrumented runtimes: %+v", job)
	}
}

// An unknown executable can still wrap the suite, so it keeps blocking.
func TestDiscoveryKeepsUnknownWrappersInNonTestJobsUnresolved(t *testing.T) {
	root := newJestRepository(t, `jobs:
  wrapper:
    steps:
      - run: ./scripts/ci-check
  task:
    steps:
      - run: make check
  action:
    steps:
      - uses: ./.github/actions/verify
`)
	writeScripts(t, root, map[string]string{"test": "mocha"})
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.Contains(t, unresolved, "ci-check")
	require.Contains(t, unresolved, "make check")
	require.Contains(t, unresolved, "/ action")
}

// Modeled on vitalets/playwright-network-cache: the version axis comes from
// another job, and only the dependency install reads it.
func TestComputedMatrixAxisStillValidatesTheTestCommand(t *testing.T) {
	root := newJestRepository(t, `jobs:
  versions:
    runs-on: ubuntu-latest
    outputs: {versions: '${{ steps.get.outputs.result }}'}
    steps:
      - uses: actions/github-script@v7
        id: get
  test:
    needs: versions
    strategy:
      matrix:
        playwrightVersion: ${{ fromJson(needs.versions.outputs.versions) }}
        os: [ubuntu-22.04]
        include:
          - {playwrightVersion: latest, os: windows-latest}
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: 20}
      - run: npm ci
      - run: npm install @playwright/test@${{ matrix.playwrightVersion }}
      - run: npx playwright install --with-deps chromium
      - run: npm test
`)
	writeScripts(t, root, map[string]string{"test": "playwright test"})
	scope, err := DiscoverValidationScope(root, "playwright")
	require.NoError(t, err)
	var validated []TestCommand
	for _, command := range scope.Commands {
		if len(command.UnvalidatedSetup) == 0 {
			validated = append(validated, command)
		}
	}
	require.Len(t, validated, 1, "%+v", scope.Commands)
	require.Contains(t, validated[0].Locations[0], "${{ matrix.playwrightVersion }}")
	// Only a dependency install reads the axis, so validation decides from the
	// job's results whether it is a caveat; discovery doesn't block on it.
	require.Equal(t, []ComputedAxis{{Workflow: ".github/workflows/test.yml", Job: "test", Axis: "playwrightVersion"}}, scope.ComputedAxes)
	require.NotContains(t, strings.Join(scope.Unresolved, "\n"), "computed at runtime")

	// Any other step reading the axis could change the tests unseen.
	other := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix:
        playwrightVersion: ${{ fromJson(needs.versions.outputs.versions) }}
    runs-on: ubuntu-latest
    steps:
      - run: echo "${{ matrix.playwrightVersion }}" > .playwright-version
      - run: npm test
`)
	writeScripts(t, other, map[string]string{"test": "playwright test"})
	scope, err = DiscoverValidationScope(other, "playwright")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Setup other than a dependency install reads a matrix axis computed at runtime")

	for _, use := range []string{"runs-on: ${{ matrix.playwrightVersion }}", "env: {PW: '${{ matrix.playwrightVersion }}'}"} {
		root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix:
        playwrightVersion: ${{ fromJson(needs.versions.outputs.versions) }}
    `+use+`
    steps:
      - run: npm test
`)
		writeScripts(t, root, map[string]string{"test": "playwright test"})
		scope, err := DiscoverValidationScope(root, "playwright")
		require.NoError(t, err)
		require.Empty(t, scope.Commands, use)
		require.Contains(t, strings.Join(scope.Unresolved, "\n"), "not a bounded static list", use)
	}
}

func TestComputedAxisReadByTheTestCommandStaysUnresolved(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix:
        shard: ${{ fromJson(needs.plan.outputs.shards) }}
    steps:
      - run: npx playwright test --shard ${{ matrix.shard }}
`)
	writeScripts(t, root, map[string]string{"test": "playwright test"})
	scope, err := DiscoverValidationScope(root, "playwright")
	require.NoError(t, err)
	for _, command := range scope.Commands {
		require.NotEmpty(t, command.UnvalidatedSetup)
	}
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "matrix.shard")
}

// Modeled on webdriverio/jasmine-boilerplate and morphatic/astrologyjs: a job
// guard on the actor or on a paths-filter output only decides whether the job
// runs at all.
func TestRuntimeContextJobGuardsAreValidatedAsPossiblePaths(t *testing.T) {
	for _, guard := range []string{"github.actor != 'dependabot[bot]'", "needs.changes.outputs.code == 'true'", "github.repository == 'example/app' && github.event_name != 'schedule'"} {
		root := newJestRepository(t, `jobs:
  test:
    if: `+guard+`
    strategy:
      matrix: {node: [20, 22]}
    steps:
      - run: npm test
        if: matrix.node != '20'
`)
		writeScripts(t, root, map[string]string{"test": "mocha"})
		scope, err := DiscoverValidationScope(root, "mocha")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved, guard)
		require.Len(t, scope.Commands, 1, guard)
		require.Len(t, scope.Commands[0].Locations, 1, "matrix.node != '20' leaves only Node 22")
		require.Contains(t, strings.Join(scope.Review, "\n"), "depends on runtime context; validated as a possible CI path", guard)
	}
}

func TestPossibleJobConditionPath(t *testing.T) {
	for _, tc := range []struct {
		condition       string
		active, assumed bool
	}{
		{"github.actor != 'dependabot[bot]'", true, true},
		{"github.event_name == 'push' && false", false, false},
		{"needs.release.outputs.created == 'true' || github.event_name == 'workflow_dispatch'", true, true},
		{"matrix.os == 'linux' && github.actor == 'bot'", false, false},
		{"github.ref == 'refs/heads/main'", true, false},
		// Sampled values can't reach every number; only an impossible guard excludes the job.
		{"github.run_number > 10", true, true},
		{"github.run_attempt > 1 && github.event_name != 'schedule'", true, true},
		{"github.run_attempt > 1 && false", false, false},
		// Array membership guards sample the array's elements.
		{`contains(fromJSON('["push", "pull_request"]'), github.event_name)`, true, true},
		{`!contains(fromJSON('["schedule"]'), github.event_name)`, true, true},
		{`contains(fromJSON('["push", "pull_request", "schedule", "workflow_dispatch", "workflow_call"]'), github.event_name)`, true, true},
		{`contains(fromJSON('[1, 2]'), github.run_number)`, true, true},
		{`contains(fromJSON('["push", 2.5]'), github.run_attempt) && github.run_attempt > 2`, true, true},
		// A bare runtime operand as a whole condition is still abstracted.
		{"github.event.pull_request.draft || github.event_name == 'push'", true, true},
		{"(github.event.pull_request.draft) && false", false, false},
		{"((github.event.pull_request.draft)) || github.event_name == 'push'", true, true},
		// Multi-line YAML guards: a newline is ordinary whitespace.
		{"github.run_number > 10 &&\n  github.event.pull_request.draft", true, true},
	} {
		active, assumed, err := possibleJobConditionPath(tc.condition, map[string]any{"os": "windows"})
		require.NoError(t, err, tc.condition)
		require.Equal(t, tc.active, active, tc.condition)
		require.Equal(t, tc.assumed, assumed, tc.condition)
	}
	// An unknown runtime value used as a function argument is never replaced
	// by a boolean, so these guards stay unresolved rather than impossible.
	for _, condition := range []string{"hashFiles('package-lock.json') != ''", "github.a == 'x' && github.b == 'y' && github.c == 'z' && github.d == 'w'",
		`contains(fromJSON('["release"]'), github.event_name) && github.run_number > 100000`,
		// A group whose value is compared is a value, not a condition.
		"(github.run_number) > 10 && false || (github.run_attempt) > 100000",
		"contains((github.event_name), 'zzz') && github.run_number > 100000"} {
		_, _, err := possibleJobConditionPath(condition, nil)
		require.Error(t, err, condition)
	}
}

func TestPowerShellArgvOnlyCommands(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "mocha"})
	for _, run := range []string{"npm ci", "npx playwright install --with-deps chromium", "npm test"} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: run, Shell: "pwsh"}, "javascript", "mocha")
		require.NotContains(t, result.Reason, "Unsupported CI shell", run)
	}
	for _, run := range []string{"npm test $env:ARGS", "npm test; echo done", "npm test | Out-Null", "npm test -- .\\spec", "& npm test", "npm test `\n  --bail", "npm ci\nnpm test"} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: run, Shell: "pwsh"}, "javascript", "mocha")
		require.Contains(t, result.Reason, "Unsupported CI shell: pwsh", run)
	}
}

func TestMayStartTests(t *testing.T) {
	for run, want := range map[string]bool{
		"sudo apt-get -y install lcov":                           false,
		"find ./coverage -name lcov.info | xargs lcov -o out":    false,
		"npm install -g npm@latest":                              false,
		"npm stage publish --tag \"$TAG\"":                       false,
		"pnpm add -g cspell && cspell \"**/*\"":                  false,
		"echo '${{ toJson(needs) }}' | jq -e 'all(.x | .y)'":     false,
		"VERSION=\"$(node -p \"require('./p').version\")\"":      false,
		"if [ -z \"$KEY\" ]; then\n  echo missing\n  exit 1\nfi": false,
		"pnpm exec vitest run test/live":                         false,
		"git diff --quiet || echo \"run `npm run docs`\"":        true,
		"npm test":               true,
		"pnpm test:bdd":          true,
		"yarn ci:all":            true,
		"./scripts/run-tests.sh": true,
		"custom-test-wrapper":    true,
		"node scripts/ci.js":     true,
		"make test":              true,
		"npx cucumber-js":        true,
		"pnpm exec playwright install && npm test": true,
		"bash -c 'npm test'":                       true,
		"node -e \"require('./scripts/test')\"":    true,
		"node --enable-source-maps scripts/ci.js":  true,
		"bash < scripts/ci.sh":                     true,
		"node --version":                           false,
		"pnpm":                                     true,
	} {
		require.Equal(t, want, mayStartTests(runtimeStep{Run: run}, "cucumber"), run)
	}
	require.True(t, mayStartTests(runtimeStep{Uses: "./.github/actions/test"}, "cucumber"))
	require.False(t, mayStartTests(runtimeStep{Run: "npm install @playwright/test@latest"}, "playwright"), "a scoped package spec is not an executable")
}

func TestNpmConfigSettingsThatChangeExecutionStayUnresolved(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "mocha"})
	for run, setup := range map[string]bool{
		"npm config set loglevel error":                          true,
		"npm config set registry=https://registry.example.test/": true,
		"npm config set @acme:registry https://npm.acme.test/":   true,
		"npm config set --location project fetch-retries 5":      true,
		"npm config set ignore-scripts true":                     false,
		"npm config set script-shell /bin/bash":                  false,
		"npm config set loglevel error node-options=--require=x": false,
	} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: run}, "javascript", "mocha")
		if setup {
			require.False(t, result.Matched, run)
			require.NotContains(t, result.Reason, "can change how test scripts execute", run)
		} else {
			require.Contains(t, result.Reason, "can change how test scripts execute", run)
		}
	}
}

func TestShellEvaluatorsMayStartTests(t *testing.T) {
	for _, run := range []string{"source ./ci/tests.sh", ". ./ci/tests.sh", "eval \"$TEST_COMMAND\""} {
		require.True(t, mayStartTests(runtimeStep{Run: run}, "cucumber"), run)
	}
}

func TestInlineNodeCodeStartsTestsOnlyWhenItCanLaunch(t *testing.T) {
	for run, want := range map[string]bool{
		"node --input-type=module -e \"import { x } from 'pkg'; console.log(x)\"":                false,
		"node -e \"require('fs').rmSync('dist',{recursive:true})\"":                              false,
		"node -e \"require('child_process').execSync('npm test')\"":                              true,
		"node -e \"require('./scripts/test')\"":                                                  true,
		"node --input-type=module -e \"import run from './scripts/run.js'; run()\"":              true,
		"node -e \"const { spawnSync } = require('node:child_process'); spawnSync('cucumber')\"": true,
	} {
		require.Equal(t, want, mayStartTests(runtimeStep{Run: run}, "cucumber"), run)
	}
}

func TestKnownPackageScriptsStartTestsOnlyThroughTheirBody(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{
		"build": "node -e \"require('fs').rmSync('dist',{recursive:true,force:true})\" && tsup && tsc -p tsconfig.build.json",
		"test":  "pnpm test:unit && pnpm test:bdd", "test:unit": "vitest run", "test:bdd": "cucumber-js",
		"docs": "npm --prefix ./website run build", "smoke": "node scripts/smoke.js",
	})
	for run, want := range map[string]bool{"pnpm build": false, "npm run build": false, "pnpm test": true, "pnpm docs": true, "pnpm smoke": true, "pnpm missing": true} {
		require.Equal(t, want, mayStartTestsIn(root, runtimeStep{Run: run}, "cucumber", 0), run)
	}
	require.True(t, mayStartTests(runtimeStep{Run: "pnpm build"}, "cucumber"), "without the package, a script stays a possible wrapper")
}

func TestCheckOnlyStepsCannotPrepareTests(t *testing.T) {
	for run, want := range map[string]bool{
		"set -euo pipefail\nVERSION=\"$(node -p \"require('./package.json').version\")\"\nif OUTPUT=\"$(npm view \"pkg@$VERSION\" version 2>&1)\"; then\n  echo exists\n  exit 1\nelif ! grep -qi \"E404\" <<< \"$OUTPUT\"; then\n  exit 1\nfi": true,
		"npm install -g npm@latest\necho \"npm $(npm --version)\"": true,
		"echo summary >> \"$GITHUB_STEP_SUMMARY\"":                 true,
		"echo NODE_ENV=test >> \"$GITHUB_ENV\"":                    false,
		"echo built > dist/marker":                                 false,
		"npm install -g typescript":                                false,
		"npm ci":                                                   false,
		"node scripts/check.js":                                    false,
	} {
		require.Equal(t, want, checkOnlyStep(run), run)
	}
	require.False(t, changesDependencies([]string{"npm", "install", "-g", "npm@latest"}), "upgrading the package manager is toolchain setup")
	require.True(t, changesDependencies([]string{"npm", "install", "-g", "typescript"}))
	require.True(t, changesDependencies([]string{"npm", "install", "npm@latest"}), "a local install changes project dependencies")
}

func TestPublishJobSetupDoesNotBlockItsCucumberTest(t *testing.T) {
	root := newJestRepository(t, `jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: 22}
      - run: |
          npm install -g npm@latest
          echo "npm $(npm --version)"
      - run: |
          set -euo pipefail
          if OUTPUT="$(npm view "pkg@1.0.0" version 2>&1)"; then
            exit 1
          fi
      - run: pnpm install --frozen-lockfile
      - run: pnpm test
      - run: pnpm build
      - run: |
          set -euo pipefail
          npm pack --dry-run
`)
	writeScripts(t, root, map[string]string{
		"test": "pnpm test:unit && pnpm test:bdd", "test:unit": "vitest run", "test:bdd": "cucumber-js",
		"build": "node -e \"require('fs').rmSync('dist',{recursive:true,force:true})\" && tsup && tsc -p tsconfig.build.json",
	})
	scope, err := DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	// The Cucumber leaf is validated; the chain's Vitest link stays outside it.
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "pnpm test:bdd", scope.Commands[0].Command)
	require.True(t, slices.ContainsFunc(scope.Review, func(review string) bool {
		return strings.Contains(review, "Other test framework is outside cucumber validation: vitest run")
	}), scope.Review)
	require.True(t, slices.ContainsFunc(scope.Review, func(review string) bool {
		return strings.Contains(review, "Check-only step before the job's cucumber test")
	}), scope.Review)
}

func TestOtherCIProvidersAreListedForReview(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npx cypress run\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "circle.yml"), []byte("workflows:\n  build:\n    jobs:\n      - cypress/run\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitlab-ci.yml"), []byte("lint:\n  script: npm run lint\n"), 0644))
	useCIProvider(t, "github")
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	var listed []string
	for _, review := range scope.Review {
		if strings.Contains(review, "not a GitHub Actions workflow") {
			listed = append(listed, review)
		}
	}
	require.Len(t, listed, 1, "only CI files that mention the framework are listed")
	require.Contains(t, listed[0], "circle.yml")
}

func TestPowerShellScopedPackagesAndQuoting(t *testing.T) {
	for run, want := range map[string]bool{
		"npm install @playwright/test@latest":        true,
		"npm install @playwright/test@1.44 lodash@4": true,
		"npm install @playwright/test":               true,
		"npx mocha --grep 'can''t'":                  false,
		"npx mocha --grep \"say \"\"hi\"\"\"":        false,
		"npm test @args":                             false,
		"npm test @(1,2)":                            false,
		"npm test -- --grep a@{b}":                   false,
	} {
		require.Equal(t, want, powershellArgvOnly("pwsh", run), run)
	}
}

func TestWindowsGuardedRowsAreReportedAsWindowsRunners(t *testing.T) {
	workflow := `jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24.14.1'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.os != 'windows-latest'
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env:
          NODE_OPTIONS: "${{ env.DD_TRACE_PACKAGE && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}"
`
	root := newJestRepository(t, workflow)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	var windows, instrumented int
	for _, command := range scope.Commands {
		if command.WindowsRunner {
			require.True(t, command.CIInstrumentationExcluded)
			windows++
		} else if !command.CIInstrumentationExcluded {
			instrumented++
		}
	}
	require.Equal(t, 1, windows, "%+v", scope.Commands)
	require.Equal(t, 1, instrumented, "%+v", scope.Commands)

	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=18"}, nil
	}, "jest")
	reasons := []string{}
	for _, job := range check.Jobs {
		reasons = append(reasons, job.Reason)
	}
	require.Contains(t, reasons, WindowsRunnerBranch, "ddtest's own Windows guard is not a fork/API-key opt-out")
	require.NotContains(t, reasons, GuardedActionBranch)
}
