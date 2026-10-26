'use strict'

// Execute the preserved action API options (or a managed background CLI) with
// instrumentation already initialized by NODE_OPTIONS. No workflow expression
// or shell fragment is evaluated here.
const fs = require('node:fs')
const path = require('node:path')
const { spawn } = require('node:child_process')
const { once } = require('node:events')
const { createRequire } = require('node:module')
const plan = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const directory = process.env.DDTEST_CYPRESS_DIRECTORY
const localRequire = createRequire(path.join(process.cwd(), 'package.json'))
const children = []
const failures = []

function start (words) {
  const command = spawn(words[0], words.slice(1), {
    stdio: ['inherit', 'pipe', 'pipe'], detached: process.platform !== 'win32',
    env: { ...process.env, PATH: path.join(process.cwd(), 'node_modules/.bin') + path.delimiter + process.env.PATH }
  })
  command.stdout?.pipe(process.stdout, { end: false })
  command.stderr?.pipe(process.stderr, { end: false })
  command.on('error', error => failures.push(error))
  command.on('exit', (code, signal) => {
    if (code !== 0) failures.push(new Error('Startup process exited: ' + words.join(' ') + ' (' + (signal || code) + ')'))
  })
  children.push(command)
}

async function waitFor (address) {
  const end = Date.now() + plan.wait_timeout_seconds * 1000
  while (Date.now() < end) {
    if (failures.length) throw failures[0]
    try {
      const response = await fetch(address, { signal: AbortSignal.timeout(1000) })
      await response.body?.cancel()
      if (response.ok) return
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error('Cypress startup readiness timed out: ' + address)
}

function waitForExit (child) {
  return new Promise(resolve => {
    const onExit = () => { clearTimeout(timer); resolve(true) }
    const timer = setTimeout(() => { child.removeListener('exit', onExit); resolve(false) }, 1000)
    child.once('exit', onExit)
  })
}

async function stop () {
  const errors = []
  for (const child of children) {
    if (!child.pid || child.exitCode !== null || child.signalCode !== null) {
      child.stdout?.destroy(); child.stderr?.destroy(); child.unref()
      continue
    }
    const signal = name => process.platform === 'win32' ? child.kill(name) : process.kill(-child.pid, name)
    try {
      let exited = waitForExit(child)
      signal('SIGTERM')
      if (!await exited) {
        exited = waitForExit(child)
        signal('SIGKILL')
        if (!await exited) throw new Error('Cypress startup did not exit after SIGKILL')
      }
    } catch (error) {
      if (error.code !== 'ESRCH') errors.push(error)
    } finally {
      // A launcher may exit while descendants retain its output descriptors.
      // Close our private pipes so API completion can reach live-owner cleanup.
      child.stdout?.destroy(); child.stderr?.destroy(); child.unref()
    }
  }
  if (errors.length) throw new AggregateError(errors, 'Cypress startup cleanup failed')
  // Live owner connections clean detached descendants after the API returns.
}

async function run () {
  try {
    for (const command of plan.start || []) start(command)
    for (const address of plan.wait_on || []) await waitFor(address)
    if (failures.length) throw failures[0]
    if (plan.cli) {
      const child = spawn(plan.cli[0], plan.cli.slice(1), { stdio: 'inherit' })
      child.on('error', error => failures.push(error))
      const [code] = await once(child, 'exit')
      if (failures.length) throw failures[0]
      process.exitCode = code === null ? 1 : code
      return
    }
    const options = { ...plan.options, record: false, parallel: false }
    const observe = require(path.join(directory, 'observer.cjs'))
    const project = observe.checkoutPath(path.resolve(options.project || '.'), 'project')
    if (typeof options.configFile === 'string' && options.configFile !== 'false') {
      options.configFile = observe.checkoutPath(path.resolve(project, options.configFile), 'configuration file')
    }
    options.configFile = observe(project, options.configFile, directory, process.env.DDTEST_CYPRESS_PROBE, options.component)
    options.project = project
    if (process.env.DDTEST_CYPRESS_PROBE) {
      options.spec = process.env.DDTEST_CYPRESS_PROBE
      options.config = observe.probeConfig(options.config || {})
      observe.clearRetryVariables(process.env)
    }
    fs.writeFileSync(path.join(directory, 'invocation.json'), JSON.stringify({
      command: process.execPath, args: [process.argv[1], process.argv[2]], api: options
    }))
    const results = await localRequire('cypress').run(options)
    if (failures.length) throw failures[0]
    if (results.status === 'failed' || results.failures) throw new Error(results.message || 'Cypress API execution failed')
    process.exitCode = results.totalFailed > 0 ? Math.min(results.totalFailed, 255) : 0
  } finally {
    await stop()
  }
}

run().catch(error => {
  fs.writeFileSync(path.join(directory, 'execution-error.txt'), String(error.message || error))
  console.error(error)
  process.exitCode = 1
})
