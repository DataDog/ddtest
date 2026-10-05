'use strict'
const fs = require('node:fs')
const path = require('node:path')
const { createRequire } = require('node:module')
const projectRequire = createRequire(path.join(process.cwd(), 'package.json'))

async function main () {
  const [action, directory, nycPath] = process.argv.slice(2)
  if (action === 'resolve') {
    let nyc
    try { nyc = projectRequire.resolve('nyc') } catch {
      throw new Error('--coverage-output requires a project-local nyc installation (npm install --save-dev nyc)')
    }
    fs.writeFileSync(directory, nyc)
    return
  }
  if (action !== 'merge') throw new Error(`Unknown coverage action: ${action}`)
  const nycRequire = createRequire(nycPath)
  const { createCoverageMap } = nycRequire('istanbul-lib-coverage')
  const merged = createCoverageMap({})
  const workers = fs.readdirSync(directory).filter(name => /^node-\d+-worker-\d+$/.test(name))
  if (!workers.length) return
  // Validate every worker before publishing a combined report. Missing or malformed
  // reports must not silently become a successful, incomplete coverage result.
  for (const worker of workers) {
    const filename = path.join(directory, worker, 'report', 'coverage-final.json')
    let report
    try { report = JSON.parse(fs.readFileSync(filename, 'utf8')) } catch (error) {
      throw new Error(`Cannot merge coverage for ${worker}: ${error.message}`)
    }
    merged.merge(report)
  }
  const temp = path.join(directory, 'merged')
  fs.mkdirSync(temp)
  fs.writeFileSync(path.join(temp, 'coverage.json'), JSON.stringify(merged.toJSON()))
  const NYC = require(nycPath)
  const nyc = new NYC({ cwd: process.cwd(), tempDirectory: temp,
    reportDir: path.join(directory, 'report'), reporter: ['json', 'lcov', 'text-summary'],
    excludeAfterRemap: false })
  await nyc.report()
  console.log(`DDTest combined coverage: ${path.join(directory, 'report')}`)
}
main().catch(error => { console.error(error.message); process.exitCode = 1 })
