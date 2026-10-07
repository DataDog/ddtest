// Run with Node 24.14.0 and an absolute path to the ddtest binary:
// node docs/examples/issue-168/reproduce.mjs /absolute/path/to/ddtest
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

const binary = resolve(process.argv[2] || './ddtest')
const expectBug = process.argv.includes('--expect-bug')
assert.ok(existsSync(binary), `Build ddtest first; binary not found: ${binary}`)
// Use the canonical path (macOS aliases /var to /private/var).
const fixture = realpathSync(mkdtempSync(join(tmpdir(), 'ddtest-issue-168-')))
console.log(`Fixture, logs, and execution records: ${fixture}`)

function write(path, content) {
  const target = join(fixture, path)
  mkdirSync(dirname(target), { recursive: true })
  writeFileSync(target, content)
}

// Disable backend/tracer use and isolate the experiment from local ddtest settings.
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
  !key.startsWith('DD_TEST_OPTIMIZATION_') && !key.startsWith('DD_TESTOPTIMIZATION_')))
Object.assign(env, {
  NODE_OPTIONS: '', DD_API_KEY: '', DD_APP_KEY: '', DD_CIVISIBILITY_ENABLED: 'false',
  DD_TRACE_ENABLED: 'false', DD_SERVICE: 'issue-168-repro',
  DD_TRACE_PACKAGE: join(fixture, 'node_modules/dd-trace/ci/init'),
  DD_TRACE_ESM_IMPORT: join(fixture, 'node_modules/dd-trace/register.js'),
})

function run(command, args, extraEnv = {}) {
  const result = spawnSync(command, args, {
    cwd: fixture, env: { ...env, ...extraEnv }, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024,
  })
  if (result.error) throw result.error
  assert.notEqual(result.status, null, `Command terminated by ${result.signal}`)
  return result
}

function setup(command, args) {
  const result = run(command, args)
  assert.equal(result.status, 0, `${command} failed:\n${result.stdout}\n${result.stderr}`)
}

write('package.json', JSON.stringify({
  private: true, type: 'module', devDependencies: { vitest: '5.0.1', 'dd-trace': '5.111.0' },
}, null, 2))
write('vitest.config.mjs', 'export default { test: { dir: "src", include: ["**/test.ts"] } };\n')
for (const name of ['endOfYear', 'eachWeekendOfYear']) {
  write(`src/${name}/test.ts`, `
import { test, expect } from 'vitest';
import { appendFileSync, writeFileSync } from 'node:fs';
test('${name}', () => {
  appendFileSync(process.env.REPRO_EVENTS, JSON.stringify({
    file: 'src/${name}/test.ts', pid: process.pid,
    session: process.env.DD_TEST_SESSION_NAME || 'direct',
  }) + '\\n');
  // Model an external resource with a unique key: a second creation must fail.
  if (process.env.REPRO_ONCE === '1')
    writeFileSync(process.env.REPRO_ONCE_DIR + '/${name}', 'created', { flag: 'wx' });
  expect(1).toBe(1);
});
`)
}
setup('git', ['init', '-q'])
setup('git', ['-c', 'user.name=Reproducer', '-c', 'user.email=reproducer@example.invalid',
  '-c', 'commit.gpgsign=false', 'commit', '--allow-empty', '-qm', 'Initialize reproducer'])
console.log('Installing pinned Vitest and tracer dependencies...')
setup('npm', ['install', '--no-audit', '--no-fund', '--cache', join(fixture, 'npm-cache')])
write('versions.txt', `Node ${process.version}\nddtest ${run(binary, ['--version']).stdout.trim()}\nVitest 5.0.1\ndd-trace 5.111.0\n`)

const vitest = ['node_modules/vitest/vitest.mjs', 'run']
const common = ['run', '--platform', 'javascript', '--framework', 'vitest',
  '--command', 'node node_modules/vitest/vitest.mjs run']
const selected = 'src/endOfYear/test.ts'
const other = 'src/eachWeekendOfYear/test.ts'
const summaries = []

function scenario(name, command, args, expectedCounts, shouldPass, once = false) {
  rmSync(join(fixture, '.testoptimization'), { recursive: true, force: true })
  const eventsPath = join(fixture, `${name}.jsonl`)
  writeFileSync(eventsPath, '')
  const onceDir = join(fixture, `${name}-resources`)
  mkdirSync(onceDir)
  const result = run(command, args, {
    REPRO_EVENTS: eventsPath, REPRO_ONCE: once ? '1' : '0', REPRO_ONCE_DIR: onceDir,
  })
  write(`${name}.log`, result.stdout + result.stderr)
  const events = readFileSync(eventsPath, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)
  const counts = Object.fromEntries([selected, other].map(file =>
    [file, events.filter(event => event.file === file).length]))
  let assignments
  const splitDir = join(fixture, '.testoptimization/runner/tests-split')
  if (existsSync(splitDir)) {
    assignments = Object.fromEntries(readdirSync(splitDir).map(file =>
      [file, readFileSync(join(splitDir, file), 'utf8').trim().split('\n').filter(Boolean)]))
    write(`${name}-assignments.json`, JSON.stringify(assignments, null, 2))
  }
  const summary = { name, exitCode: result.status, counts, events, assignments }
  summaries.push(summary)
  write('results.json', JSON.stringify(summaries, null, 2))
  console.log(`${name}: exit=${result.status}, endOfYear=${counts[selected]}, eachWeekendOfYear=${counts[other]}`)
  assert.deepEqual(counts, { [selected]: expectedCounts[0], [other]: expectedCounts[1] },
    `Unexpected executions; inspect ${name}.log.`)
  assert.equal(result.status === 0, shouldPass, `Unexpected exit status; inspect ${name}.log`)
  return result
}

scenario('baseline', process.execPath, vitest, [1, 1], true)
scenario('relative-filter', process.execPath, [...vitest, selected], [1, 1], true)
scenario('absolute-filter', process.execPath, [...vitest, join(fixture, selected)], [1, 1], true)
const parallel = scenario('ddtest-parallel', binary,
  [...common, '--min-parallelism', '2', '--max-parallelism', '2'], expectBug ? [1, 2] : [1, 1], true)
assert.match(parallel.stdout + parallel.stderr, /Test files run: 2/)
const assignmentFiles = Object.values(summaries.at(-1).assignments)
assert.equal(assignmentFiles.length, 2)
assert.ok(assignmentFiles.every(files => files.length === 1))
assert.deepEqual(assignmentFiles.flat().sort(), [selected, other].sort())
scenario('ddtest-explicit-selection', binary,
  [...common, '--min-parallelism', '1', '--max-parallelism', '1', '--', selected], expectBug ? [1, 1] : [1, 0], true)
scenario('ddtest-exclude', binary,
  [...common, '--min-parallelism', '1', '--max-parallelism', '1',
    '--tests-exclude-pattern', other], expectBug ? [1, 1] : [1, 0], true)
scenario('baseline-create-once', process.execPath, vitest, [1, 1], true, true)
const createOnce = scenario('ddtest-create-once', binary,
  [...common, '--min-parallelism', '2', '--max-parallelism', '2'], expectBug ? [1, 2] : [1, 1], !expectBug, true)
if (expectBug) assert.match(createOnce.stdout + createOnce.stderr, /EEXIST/)
console.log(expectBug
  ? 'Confirmed: disjoint assignments expand into duplicate execution; create-once test fails only under ddtest.'
  : 'Verified: exact assignments, explicit selection, exclusion, and create-once tests all pass.')
console.log(`Evidence retained in ${fixture}; remove that directory when finished.`)
