'use strict'
// Cypress's coverage plugin reads NYC configuration independently of the NYC CLI.
// Override its resolved options before task.js captures them, without editing the
// project's config or relying on NYC_* variables that this plugin does not read.
const Module = require('node:module')
const fs = require('node:fs')
const path = require('node:path')
const directory = process.env.DDTEST_WORKER_COVERAGE_DIRECTORY
if (directory) {
  const load = Module._load
  Module._load = function (request, parent, isMain) {
    const exported = load.apply(this, arguments)
    if (request !== './task-utils') return exported
    const filename = Module._resolveFilename(request, parent)
    if (!filename.replace(/\\/g, '/').endsWith('/@cypress/code-coverage/task-utils.js')) return exported
    if (typeof exported.readNycOptions !== 'function') throw new Error('Unsupported @cypress/code-coverage: readNycOptions is unavailable')
    return { ...exported, readNycOptions (cwd) {
      const packagePath = path.join(cwd, 'package.json')
      const pkg = fs.existsSync(packagePath) ? JSON.parse(fs.readFileSync(packagePath, 'utf8')) : {}
      if (pkg.scripts && pkg.scripts['coverage:report']) {
        throw new Error('--coverage-output does not support a custom Cypress coverage:report script; configure reporters through NYC instead')
      }
      return { ...exported.readNycOptions(cwd), 'temp-dir': path.join(directory, 'raw'),
        'report-dir': path.join(directory, 'report'), reporter: ['json'] }
    } }
  }
}
