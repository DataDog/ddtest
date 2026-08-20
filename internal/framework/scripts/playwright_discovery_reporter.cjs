const path = require('path')

class DDTestPlaywrightDiscoveryReporter {
  printsToStdio() {
    return false
  }

  onError(error) {
    const message = error && error.message ? error.message : String(error)
    process.stdout.write('__DDTEST_PLAYWRIGHT_ERROR__' + JSON.stringify({ message }) + '\n')
  }

  onBegin(config, suite) {
    // Dependency and teardown projects are shared setup, not independently
    // shardable specs. Playwright runs them automatically with the selected
    // primary project, so putting them in DDTest's plan would run them twice.
    const projectSuites = suite.suites.filter(child => child.project())
    const dependencyProjects = new Set()
    for (const projectSuite of projectSuites) {
      const project = projectSuite.project()
      for (const dependency of project.dependencies || [])
        dependencyProjects.add(typeof dependency === 'string' ? dependency : dependency.name)
      if (project.teardown)
        dependencyProjects.add(typeof project.teardown === 'string' ? project.teardown : project.teardown.name)
    }
    const primarySuites = projectSuites.filter(projectSuite => !dependencyProjects.has(projectSuite.project().name))
    const tests = primarySuites.length ? primarySuites.flatMap(projectSuite => projectSuite.allTests()) : suite.allTests()
    const files = tests.map(test => path.relative(process.cwd(), test.location.file))
    process.stdout.write('__DDTEST_PLAYWRIGHT_FILES__' + JSON.stringify({ rootDir: config.rootDir, files }) + '\n')
  }
}

module.exports = DDTestPlaywrightDiscoveryReporter
