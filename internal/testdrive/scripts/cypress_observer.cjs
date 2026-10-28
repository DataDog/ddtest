'use strict'

// Observe Cypress's public after:run results inside the original CLI command,
// retaining package aliases, server orchestration, reporters and user hooks.
const fs = require('node:fs')
const path = require('node:path')
const { pathToFileURL } = require('node:url')
// Runs execute in a disposable copy of the checkout. A path into the original
// checkout is moved into the copy, and nothing outside the checkout is wrapped,
// so a generated wrapper never lands beside customer files.
function checkoutPath (file, label) {
  const checkout = process.env.DDTEST_CYPRESS_CHECKOUT
  if (!checkout) return file
  const real = candidate => {
    try { return fs.realpathSync(candidate) } catch {}
    try { return path.join(fs.realpathSync(path.dirname(candidate)), path.basename(candidate)) } catch { return candidate }
  }
  const within = (root, candidate) => {
    const relative = path.relative(root, candidate)
    return relative === '' || (relative !== '..' && !relative.startsWith('..' + path.sep) && !path.isAbsolute(relative))
  }
  const source = process.env.DDTEST_CYPRESS_SOURCE_ROOT
  if (source) {
    for (const root of new Set([source, real(source)])) {
      if (root !== checkout && within(root, file)) {
        file = path.join(checkout, path.relative(root, file))
        break
      }
    }
  }
  // Compare real paths: symlinks must not escape, and /var may be /private/var.
  if (!within(real(checkout), real(file))) {
    throw new Error('Cypress ' + label + ' resolves outside checkout; review ' + file)
  }
  return file
}
function createObservedConfig (project, requested, directory, probe, component) {
  if (component) throw new Error('Cypress component testing is not supported by ddtest')
  if (requested === 'false') throw new Error('Cypress validation requires the original configuration')
  const original = requested ? path.resolve(project, requested) :
    ['cypress.config.ts', 'cypress.config.js', 'cypress.config.mjs', 'cypress.config.cjs']
      .map(file => path.join(project, file)).find(file => fs.existsSync(file))
  if (!original || !fs.existsSync(original)) throw new Error('Cypress configuration was not found')
  checkoutPath(original, 'configuration file')

  let esm = path.extname(original) === '.mjs'
  if (path.extname(original) === '.js') {
    let ancestor = path.dirname(original)
    while (true) {
      const pkg = path.join(ancestor, 'package.json')
      if (fs.existsSync(pkg)) { esm = JSON.parse(fs.readFileSync(pkg, 'utf8')).type === 'module'; break }
      const parent = path.dirname(ancestor)
      if (parent === ancestor) break
      ancestor = parent
    }
  }
  const extension = path.extname(original) === '.ts' ? '.ts' : esm ? '.mjs' : '.cjs'
  const wrapper = path.join(path.dirname(original), 'ddtest-observed-' + require('node:crypto').randomUUID() + extension)
  const imported = esm || extension === '.ts'
    ? 'import original from ' + JSON.stringify(extension === '.ts' ? './' + path.basename(original) : pathToFileURL(original).href) + ';\nimport fs from "node:fs";\n'
    : 'const original = require(' + JSON.stringify(original) + ');\nconst fs = require("node:fs");\n'
  function attachObserver (original, options) {
    const type = options.component ? 'component' : 'e2e'
    const config = { ...original, [type]: { ...original[type] } }
    // Disable native retries before setup, so instrumentation can subsequently
    // apply the retry budget returned by the local feature settings.
    if (options.probe) { config.retries = 0; config[type].retries = 0 }
    const setup = config[type].setupNodeEvents
    config[type].setupNodeEvents = async function (on, effective) {
      const handlers = []
      const observe = (event, handler) => event === 'after:run' ? handlers.push(handler) : on(event, handler)
      const returned = setup ? await setup.call(this, observe, effective) : effective
      const resolved = returned || effective
      // Coverage of the code Cypress itself loads is not application coverage.
      const projectRoot = resolved.projectRoot || effective.projectRoot
      const supportFile = resolved.supportFile === undefined ? effective.supportFile : resolved.supportFile
      // A customer setupNodeEvents has the final word on config. Only the
      // auto-retries probe lets the tracer apply a native retry budget.
      if (options.probe && options.probeMode !== 'fail-once') resolved.retries = 0
      let probeExecutions = 0
      if (options.probe) {
        // Cypress 16 removed Cypress.env() in specs; its replacement reads expose.
        if (parseInt(effective.version, 10) >= 16) {
          resolved.expose = { ...resolved.expose, __ddtest_probe_mode: options.probeMode }
        } else {
          resolved.env = { ...resolved.env, __ddtest_probe_mode: options.probeMode }
        }
        on('task', { 'ddtest:validation:attempt': () => { probeExecutions++; return null } })
      }
      on('after:run', async results => {
        try {
          for (const handler of handlers) await handler(results)
        } finally {
          fs.writeFileSync(options.directory + '/results.json', JSON.stringify({
            ...results, complete: true, configNode: process.versions.node,
            componentConfigured: !!original.component,
            projectRoot, supportFile: typeof supportFile === 'string' ? supportFile : undefined,
            probeExecutions: options.probe ? probeExecutions : undefined
          }))
        }
      })
      return resolved
    }
    return config
  }
  const options = JSON.stringify({ directory, probe: !!probe, component, probeMode: process.env.DDTEST_PROBE_MODE })
  const body = 'const config = (' + attachObserver.toString() + ')(original, ' + options + ');\n'
  // Record the wrapper before writing it, so ddtest removes it after the run.
  fs.appendFileSync(path.join(directory, 'wrappers'), wrapper + '\n')
  fs.writeFileSync(wrapper, imported + body + (esm || extension === '.ts' ? '\nexport default config;\n' : '\nmodule.exports = config;\n'))
  return wrapper
}
// Preserve Cypress's serialized syntax instead of maintaining a second parser.
// JSON options can be edited directly; key=value options use last-value wins.
function probeConfig (config) {
  let parsed = config
  if (typeof config === 'string') {
    try { parsed = JSON.parse(config) } catch { return config + ',retries=0' }
  }
  parsed = { ...parsed, retries: 0 }
  for (const type of ['e2e', 'component']) {
    if (parsed[type]) parsed[type] = { ...parsed[type], retries: 0 }
  }
  return typeof config === 'string' ? JSON.stringify(parsed) : parsed
}
// Cypress reads CYPRESS_RETRIES in any case; probes never inherit it.
function clearRetryVariables (env) {
  for (const name of Object.keys(env)) {
    if (name.toUpperCase() === 'CYPRESS_RETRIES') delete env[name]
  }
}
module.exports = createObservedConfig
module.exports.checkoutPath = checkoutPath
module.exports.probeConfig = probeConfig
module.exports.clearRetryVariables = clearRetryVariables

const cli = process.argv[1] || ''
if (/[/\\](?:cypress[/\\]bin[/\\]cypress|\.bin[/\\]cypress)(?:\.js)?$/.test(cli) &&
    process.argv[2] === 'run' && process.env.DDTEST_CYPRESS_DIRECTORY) {
  const directory = process.env.DDTEST_CYPRESS_DIRECTORY
  const args = process.argv.slice(3)
  function take (names) {
    let value
    for (let i = 0; i < args.length; i++) {
      if (args[i] === '--') break
      const option = names.find(name => args[i] === name || args[i].startsWith(name + '=') ||
        (name.length === 2 && args[i].startsWith(name) && args[i].length > 2))
      if (!option) continue
      const item = args.splice(i, 1)[0]
      value = item === option ? args.splice(i, 1)[0] : item.slice(option.length).replace(/^=/, '')
      if (value === undefined) throw new Error(option + ' requires a value')
      i--
    }
    return value
  }
  const project = checkoutPath(path.resolve(process.cwd(), take(['--project', '-P']) || '.'), 'project')
  let requested = take(['--config-file', '-C'])
  if (requested && requested !== 'false') requested = checkoutPath(path.resolve(project, requested), 'configuration file')
  // Never contact Cypress Cloud from a local mock trial.
  take(['--group', '--ci-build-id', '--key', '-k', '--tag', '-t', '--auto-cancel-after-failures'])
  for (let i = args.length - 1; i >= 0; i--) {
    if (args[i] === '--record' || args[i] === '--parallel') {
      if (args[i + 1] === 'true' || args[i + 1] === 'false') args.splice(i + 1, 1)
      args.splice(i, 1)
    } else if (args[i].startsWith('--record=') || args[i].startsWith('--parallel=')) args.splice(i, 1)
  }
  const probe = process.env.DDTEST_CYPRESS_PROBE
  function appendOptions (...options) {
    const end = args.indexOf('--')
    args.splice(end < 0 ? args.length : end, 0, ...options)
  }
  if (probe) {
    take(['--spec', '-s'])
    const config = take(['--config', '-c'])
    appendOptions('--config', config ? probeConfig(config) : 'retries=0')
    appendOptions('--spec', probe)
    clearRetryVariables(process.env)
  }
  const wrapper = createObservedConfig(project, requested, directory, probe, args.includes('--component'))
  appendOptions('--project', project, '--config-file', wrapper)
  fs.writeFileSync(path.join(directory, 'invocation.json'), JSON.stringify({
    command: process.execPath, args: [...process.execArgv, cli, 'run', ...args]
  }))
  process.argv.splice(3, process.argv.length - 3, ...args)
}
