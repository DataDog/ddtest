const fs = require('node:fs')

// Use Playwright's public reporter API for both --list and execution. The file
// is private session data; only aggregate counts survive in the final report.
class ValidationReporter {
  constructor () { this.errors = [] }
  printsToStdio () { return false }
  onBegin (config, suite) { this.config = config; this.suite = suite }
  onError (error) { this.errors.push(error.message || String(error)) }
  onEnd () {
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
      version: this.config && this.config.version,
      projects: this.config ? this.config.projects.map(project => ({
        name: project.name, root: project.testDir, dependencies: project.dependencies || [], teardown: project.teardown
      })) : [], tests, errors: this.errors
    }))
  }
}
module.exports = ValidationReporter
