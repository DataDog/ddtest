# Onboarding configuration checks

## Check Jest configuration before test execution

With the project's test dependencies installed, run:

```sh
ddtest testdrive --framework jest --check-only --yes
```

This loads the effective Jest configuration with `--showConfig` (which can execute
project configuration code), checks the Jest/Circus runner and local Node version
against the selected tracer, and reads GitHub Actions setup, conditions and runtime
matrices. It resolves the action's default tracer from that action's metadata,
compares its Node requirements with CI, and checks that local and CI selections
agree. Unknown expressions and unavailable metadata remain unvalidated.

A project tracer takes precedence. If it is absent, `--tracer-version` selects a
fallback candidate from npm metadata; no tracer is installed in this mode. An
advisory recommendation may suggest a supported tracer line covering more of the
existing CI matrix. It never changes dependencies or excludes test coverage.
Metadata checks require access to the configured npm registry and, when relevant,
GitHub action metadata and Node release metadata. No Datadog credentials are used.

The result is saved to `.testoptimization/testdrive.json`. Exit 0 and
`checks_passed: true` mean these configuration checks passed. `success` and
`local_success` remain false, `runs` is empty, and compatibility, advanced features,
actual CI execution and Datadog backend processing remain untested. An incompatible
or unresolved check returns non-zero and still writes its findings. Ordinary
execution retains its existing HTML report; check-only does not generate one.

### Manual CLI QA

Prerequisites: build `ddtest` from this branch, install Git, and use Node 22 or 24 with npm.
Run the following in a new shell from the ddtest repository root. This creates
only a disposable fixture and removes it when that shell exits. The installed
packages below prepare the fixture; check-only itself must not install a tracer.

```sh
task_ddtest_bin="$PWD/ddtest"
task_preflight_dir=$(mktemp -d)
trap 'rm -rf "$task_preflight_dir"' EXIT
cd "$task_preflight_dir"
git init -q
cat > package.json <<'JSON'
{"scripts":{"test":"jest"},"devDependencies":{"jest":"30"}}
JSON
npm install --no-save --package-lock=false --no-audit --no-fund jest@30 dd-trace@5
cat > jest.config.js <<'JS'
module.exports = {testEnvironment: 'node', testRunner: 'jest-circus/runner'}
JS
cat > never.test.js <<'JS'
require('fs').writeFileSync('test-executed', 'unexpected')
test('must not run during check-only', () => { throw new Error('must not run') })
JS
"$task_ddtest_bin" testdrive --framework jest --check-only --yes
node -e "const r=require('./.testoptimization/testdrive.json'); if (!r.checks_passed || r.success || r.local_success || (r.runs || []).length || r.tracer_selection.source !== 'project') process.exit(1)"
test ! -e test-executed
```

Expect exit 0, a compatible local preflight, CI marked `not applicable`, project
tracer precedence, and no `test-executed` file. The JSON must explicitly say that
compatibility and features were not exercised.

Now add a CI job with no Datadog action:

```sh
mkdir -p .github/workflows
cat > .github/workflows/ci.yml <<'YAML'
jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22'}
      - run: npm test
YAML
if "$task_ddtest_bin" testdrive --framework jest --check-only --yes; then
  echo 'Unexpected success for missing CI instrumentation' >&2
  exit 1
fi
node -e "const r=require('./.testoptimization/testdrive.json'); if (r.checks_passed || !r.ci_runtime.jobs.some(j => j.code === 'missing_instrumentation')) process.exit(1)"
test ! -e test-executed
```

Expect non-zero and `missing_instrumentation` for the CI step. The workflow and
test files must remain unchanged; a passing local configuration cannot certify CI.

Now add the Datadog action using the same exact tracer release as the fixture:

```sh
node <<'JS'
const fs = require('fs')
const version = require('./node_modules/dd-trace/package.json').version
fs.writeFileSync('.github/workflows/ci.yml', [
  'jobs:', '  test:', '    steps:',
  '      - uses: actions/setup-node@v4',
  "        with: {node-version: '22'}",
  '      - uses: datadog/test-visibility-github-action@v3',
  "        with: {languages: js, js-tracer-version: '" + version + "'}",
  '      - run: npm test',
  "        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}", ''
].join('\n'))
JS
"$task_ddtest_bin" testdrive --framework jest --check-only --yes
node -e "const r=require('./.testoptimization/testdrive.json'); if (!r.checks_passed || r.ci_runtime.status !== 'compatible' || r.ci_tracer_selection.status !== 'compatible' || r.success) process.exit(1)"
test ! -e test-executed
```

Expect exit 0, compatible static CI runtimes and matching tracer selections. This
still does not execute CI or claim successful onboarding.

Finally, remove the fixture's CI workflow, move its tracer aside, and check a
fallback candidate:

```sh
rm .github/workflows/ci.yml
mv node_modules/dd-trace saved-dd-trace
"$task_ddtest_bin" testdrive --framework jest --check-only --tracer-version 5 --yes
node -e "const r=require('./.testoptimization/testdrive.json'); if (!r.checks_passed || r.tracer_selection.source !== 'fallback' || !r.tracer_selection.resolved_version || r.success) process.exit(1)"
test ! -d node_modules/dd-trace
test ! -e test-executed
```

Expect exit 0, an exact resolved fallback version, and no new tracer installation
or test execution. Exit the QA shell to remove the disposable fixture.
