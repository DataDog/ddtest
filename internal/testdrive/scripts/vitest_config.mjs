import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const require = createRequire(resolve('package.json'))
const { createVitest, parseCLI } = await import(pathToFileURL(require.resolve('vitest/node')).href)
const { version } = require('vitest/package.json')
const major = Number(version.split('.')[0])
const { filter, options } = parseCLI(['vitest', ...JSON.parse(process.argv[1])])
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
