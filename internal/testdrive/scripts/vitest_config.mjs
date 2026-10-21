import { createVitest, parseCLI, version } from 'vitest/node'

const { filter, options } = parseCLI(['vitest', ...JSON.parse(process.argv[1])])
const vitest = await createVitest('test', { ...options, watch: false }, {
  cacheDir: process.argv[2],
})
try {
  const specifications = await vitest.globTestSpecifications(filter)
  console.log('__DDTEST_VITEST_CONFIG__' + JSON.stringify({
    version,
    projects: vitest.projects.map(project => ({
      name: project.name,
      root: project.config.root,
      browser: !!project.config.browser?.enabled,
      pool: project.config.pool,
      typecheck: !!project.config.typecheck?.enabled,
    })),
    files: specifications.map(spec => spec.moduleId),
  }))
} finally {
  await vitest.close()
}
