const fs = require('node:fs')
const { createRequire } = require('node:module')
const path = require('node:path')
const cliRequire = createRequire(fs.realpathSync(process.argv[1]))
const playwrightRoot = path.dirname(cliRequire.resolve('playwright/package.json'))
const lineReporterPath = path.join(playwrightRoot, 'lib/reporters/line.js')
// Playwright 1.60 bundles its reporters into lib/runner and exports only their
// TerminalReporter base. Its epilogue prints the same summary as line.js, which
// is where the tracer reports disabled tests.
const LineReporter = fs.existsSync(lineReporterPath)
  ? require(lineReporterPath).default
  : class extends require(path.join(playwrightRoot, 'lib/runner/index.js')).base.TerminalReporter {
    async onEnd (result) { await super.onEnd(result); this.epilogue(false) }
  }

// Use Playwright's public reporter API for both --list and execution. The file
// is private session data; only aggregate counts survive in the final report.
// The builtin terminal summary is also where dd-trace reports disabled tests.
// Add it alongside customer reporters, including configurations with only custom
// reporters; otherwise a native skip could have no corresponding telemetry.
class ValidationReporter extends LineReporter {
  constructor (options) { super(options); this.errors = [] }
  // Expose the original public reporter callbacks across Playwright versions.
  version () { return 'v1' }
  onBegin (config, suite) {
    if (super.version?.() === 'v2') { super.onConfigure(config); super.onBegin(suite) }
    else super.onBegin(config, suite)
    this.config = config; this.suite = suite
  }
  onError (error) { super.onError(error); this.errors.push(error.message || String(error)) }
  async onEnd (result) {
    await super.onEnd(result)
    const tests = this.suite ? this.suite.allTests().map(test => {
      const project = test.parent.project()
      const titles = test.titlePath().slice(3)
      const name = [...titles.slice(0, -1).filter(Boolean), test.title].join(' ')
      const attempts = test.results.map(result => ({
        status: result.status,
        retry: result.retry,
        failure: (result.errors || []).map(error => error.message || String(error)).join('\n')
      }))
      return { file: test.location.file, name, project: project.name,
        browser: project.use.browserName || project.use.defaultBrowserType || 'chromium', repeat: test.repeatEachIndex,
        expectedStatus: test.expectedStatus, attempts,
        status: attempts.length ? attempts[attempts.length - 1].status : 'skipped' }
    }) : []
    fs.writeFileSync(process.env.DDTEST_PLAYWRIGHT_REPORT, JSON.stringify({
      node: process.versions.node, version: this.config && this.config.version,
      projects: this.config ? this.config.projects.map(project => ({
        name: project.name, root: project.testDir, dependencies: project.dependencies || [], teardown: project.teardown
      })) : [], tests, errors: this.errors
    }))
  }
}
module.exports = ValidationReporter
