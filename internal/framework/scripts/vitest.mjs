import { readFileSync, realpathSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const request = JSON.parse(readFileSync(new URL('./request.json', import.meta.url), 'utf8'))
// Resolve the project's Vitest, even though this adapter lives in a temp directory.
const require = createRequire(resolve('package.json'))
const { createVitest } = await import(pathToFileURL(require.resolve('vitest/node')).href)
const major = Number(require('vitest/package.json').version.split('.')[0])
const runner = major >= 3
  ? await import('./vitest_modern.mjs')
  : await import('./vitest_legacy.mjs')

// Match the test environment before loading the user's configuration.
process.env.TEST = 'true'
process.env.VITEST = 'true'
process.env.NODE_ENV ??= 'test'
const config = { config: request.config, run: true, watch: false }
const context = major >= 5
  ? await createVitest(config)
  : await createVitest('test', config)

try {
  // Discovery only loads configuration; execution also initializes reporters
  // and coverage. Neither step starts tests before assignment filtering.
  if (!request.discover) await runner.initialize(context)
  const discovered = await runner.discoverSpecifications(context)
  if (request.discover) {
    writeFileSync(new URL('./files.json', import.meta.url), JSON.stringify(discovered.map(runner.filePath)))
  } else {
    const canonicalPath = file => realpathSync(resolve(file))
    const selected = new Set(request.files.map(canonicalPath))
    const specs = discovered.filter(spec => selected.has(canonicalPath(runner.filePath(spec))))
    if (specs.length === 0) {
      console.error('No assigned Vitest test files found')
      process.exitCode = context.config.passWithNoTests ? 0 : 1
    } else {
      await runner.runSpecifications(context, specs)
    }
  }
} finally {
  await context.close()
}
