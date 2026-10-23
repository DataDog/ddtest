'use strict'

// Observe Mocha's native events without replacing the customer's reporter.
// Each process/Runner owns a file so an incomplete run cannot look like success.
const fs = require('node:fs')
const path = require('node:path')
const { inspect } = require('node:util')
// Only the first process reaching an actual runner owns native results.
// Its children may run deliberate crashing Mocha fixtures; exclude those too.
if (process.env.DDTEST_MOCHA_OWNER && process.env.DDTEST_MOCHA_OWNER !== String(process.pid)) return
let root
try {
  const paths = [process.argv[1] && path.dirname(path.resolve(process.argv[1])), process.cwd()].filter(Boolean)
  root = path.dirname(require.resolve('mocha/package.json', { paths }))
} catch (error) {
  if (error.code !== 'MODULE_NOT_FOUND') throw error
  return
}
function internal (file) {
  const base = path.join(root, 'lib', file)
  return require(fs.existsSync(base + '.js') ? base + '.js' : base + '.cjs')
}
const Runner = internal('runner')
let sequence = 0
const observed = new WeakSet()
const entry = process.argv[1] && path.resolve(process.argv[1])
const mochaWorker = entry === path.join(root, 'lib/nodejs/worker.js') || entry === path.join(root, 'lib/nodejs/worker.cjs')
function observeRunner (prototype) {
  const originalRun = prototype.run
  prototype.run = function (...args) {
    // Worker events are also forwarded to the parent Runner. Do not count twice.
    if (mochaWorker || process.env.MOCHA_WORKER_ID !== undefined || observed.has(this)) return originalRun.apply(this, args)
    process.env.DDTEST_MOCHA_OWNER = String(process.pid)
    observed.add(this)
    const file = path.join(process.env.DDTEST_MOCHA_RESULTS, `${process.pid}-${sequence++}.json`)
    // With delay, Mocha waits for a global run() call before it starts.
    const report = { complete: false, delayed: Boolean(this._opts && this._opts.delay), tests: [], errors: [] }
    const save = () => fs.writeFileSync(file, JSON.stringify(report))
    save()
    const failure = error => {
      if (!error) return ''
      let message = error.stack || error.message || String(error)
      for (const key of ['actual', 'expected', 'operator']) {
        if (Object.prototype.hasOwnProperty.call(error, key)) message += `\n${key}: ${inspect(error[key], { depth: 3, sorted: true })}`
      }
      return message
    }
    const record = (test, status, error) => {
      if (test.type !== 'test') {
        if (error) report.errors.push(`${test.fullTitle()}: ${error.message || error}`)
        return
      }
      report.tests.push({ file: typeof test.file === 'string' && test.file ? path.resolve(test.file) : '', name: test.fullTitle(), status, failure: failure(error) })
    }
    const pass = test => record(test, 'passed')
    const pending = test => record(test, 'pending')
    const fail = (test, error) => record(test, 'failed', error)
    this.on('pass', pass)
    this.on('pending', pending)
    this.on('fail', fail)
    this.once('end', () => {
      report.complete = true; save()
      this.removeListener('pass', pass); this.removeListener('pending', pending); this.removeListener('fail', fail)
      observed.delete(this)
    })
    return originalRun.apply(this, args)
  }
}
// Claim ownership before test files load: a fixture spawned while loading
// tests or setup would otherwise become an owner of its own.
{
  const Mocha = internal('mocha')
  for (const name of ['loadFiles', 'loadFilesAsync', 'run']) {
    const original = Mocha.prototype[name]
    if (!original) continue
    Mocha.prototype[name] = function (...args) {
      if (!mochaWorker && process.env.MOCHA_WORKER_ID === undefined) process.env.DDTEST_MOCHA_OWNER ??= String(process.pid)
      return original.apply(this, args)
    }
  }
}
observeRunner(Runner.prototype)
// ParallelBufferedRunner overrides run(); its public events aggregate workers.
const parallel = path.join(root, 'lib/nodejs/parallel-buffered-runner')
if (fs.existsSync(parallel + '.js') || fs.existsSync(parallel + '.cjs')) {
  observeRunner(internal('nodejs/parallel-buffered-runner').prototype)
}

if (process.env.DDTEST_MOCHA_PROBE) {
  const Mocha = internal('mocha')
  const selected = new WeakSet()
  const selectProbe = mocha => {
    // Apply selection once. The tracer may subsequently remove the file for
    // TIA; a later loadFiles call must not put a skipped probe back.
    if (selected.has(mocha)) return
    selected.add(mocha)
    if (!['bdd', 'tdd'].includes(mocha.options.ui || 'bdd')) throw new Error('ddtest Mocha probes currently require the bdd or tdd UI')
    mocha.files = [...new Set([].concat(mocha.options.file || []).map(file => path.resolve(file)).concat(process.env.DDTEST_MOCHA_PROBE))]
    mocha.options.grep = /^ddtest validation probe$/
    mocha.options.invert = false
    mocha.options.watch = false
    mocha.options.delay = false
    mocha.suite.delayed = false
    mocha.suite.retries(0)
  }
  // Programmatic users can load files explicitly or let run() load them.
  // Preserve callbacks, requires, hooks, UI, reporter and execution options.
  for (const name of ['loadFiles', 'loadFilesAsync', 'run']) {
    const original = Mocha.prototype[name]
    if (!original) continue
    Mocha.prototype[name] = function (...args) {
      selectProbe(this)
      return original.apply(this, args)
    }
  }
  // Change only file/name selection for controlled probes; retain effective
  // config, UI, requires, hooks, parallel setting and ordinary runner options.
  const options = internal('cli/options')
  const loadOptions = options.loadOptions
  options.loadOptions = function (...args) {
    const result = loadOptions.apply(this, args)
    if (!result) return result
    if (result.ui && !['bdd', 'tdd'].includes(result.ui)) throw new Error('ddtest Mocha probes currently require the bdd or tdd UI')
    result._ = [process.env.DDTEST_MOCHA_PROBE]
    delete result.spec
    delete result.fgrep
    result.grep = '^ddtest validation probe$'
    result.invert = false
    result.retries = 0
    result.watch = false
    result.delay = false
    return result
  }
}
