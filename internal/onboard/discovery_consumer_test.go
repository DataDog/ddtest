// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
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
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "matrix axis playwrightVersion is computed at runtime")
	require.NotContains(t, strings.Join(scope.Unresolved, "\n"), "not a bounded static list")

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
	} {
		active, assumed, err := possibleJobConditionPath(tc.condition, map[string]any{"os": "windows"})
		require.NoError(t, err, tc.condition)
		require.Equal(t, tc.active, active, tc.condition)
		require.Equal(t, tc.assumed, assumed, tc.condition)
	}
	for _, condition := range []string{"hashFiles('package-lock.json') != ''", "github.a == 'x' && github.b == 'y' && github.c == 'z' && github.d == 'w'"} {
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
