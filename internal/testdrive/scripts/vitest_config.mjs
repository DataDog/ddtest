import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const require = createRequire(resolve('package.json'))
const { createVitest, parseCLI } = await import(pathToFileURL(require.resolve('vitest/node')).href)
const { version } = require('vitest/package.json')
const major = Number(version.split('.')[0])
const originalArguments = JSON.parse(process.argv[1])
const parsed = parseCLI(['vitest', ...originalArguments])
let { filter, options } = parsed
let probeArguments
if (process.argv[3]) {
  // Use the installed CLI parser to distinguish positional file filters from
  // option values, even when both contain the same text. Parsing the marker
  // never loads configuration or executes tests. NUL cannot occur in real argv.
  const marker = '\0ddtest positional filter\0'
  const separator = originalArguments.indexOf('--')
  const before = separator < 0 ? originalArguments : originalArguments.slice(0, separator)
  probeArguments = before.filter((argument, index) => {
    if (argument.startsWith('-') || !filter.includes(argument) || index === 0 && ['run', 'watch', 'dev', 'related'].includes(argument)) return true
    const candidate = [...before]
    candidate[index] = marker
    try { return !parseCLI(['vitest', ...candidate]).filter.includes(marker) } catch { return true }
  })
  probeArguments.push(process.argv[3])
  const parsedProbe = parseCLI(['vitest', ...probeArguments])
  filter = parsedProbe.filter
  options = parsedProbe.options
}
process.env.TEST = 'true'
process.env.VITEST = 'true'
process.env.NODE_ENV ??= 'test'
const config = { ...options, run: true, watch: false }
const vite = { cacheDir: process.argv[2] }
const vitest = major >= 5
  ? await createVitest(config, vite)
  : await createVitest('test', config, vite)
try {
  const specifications = major >= 3
    ? await vitest.globTestSpecifications(filter)
    : await vitest.globTestFiles(filter)
  console.log('__DDTEST_VITEST_CONFIG__' + JSON.stringify({
    version,
    probeArguments,
    node: process.version,
    executable: process.execPath,
    projects: vitest.projects.map(project => ({
      name: project.name ?? project.getName(),
      root: project.config.root,
      browser: !!project.config.browser?.enabled,
      pool: project.config.pool,
      typecheck: !!project.config.typecheck?.enabled,
    })),
    files: specifications.map(spec => spec.moduleId ?? spec[1]),
  }))
} finally {
  await vitest.close()
}
