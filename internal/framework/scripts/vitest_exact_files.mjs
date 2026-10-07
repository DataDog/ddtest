import { readFileSync, realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { basename, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const entrypoint = process.argv[1] || ''
const filesPath = process.env.DDTEST_VITEST_SELECTED_FILES

// Package managers inherit NODE_OPTIONS. Wait for the actual Vitest process,
// then clear the request so its test workers do not install the adapter again.
if (filesPath && ['vitest', 'vitest.mjs', 'vitest.js'].includes(basename(entrypoint))) {
  delete process.env.DDTEST_VITEST_SELECTED_FILES
  const canonicalPath = file => realpathSync(resolve(file))
  const selected = new Set(JSON.parse(readFileSync(filesPath, 'utf8')).map(canonicalPath))
  const require = createRequire(pathToFileURL(realpathSync(entrypoint)))
  const { startVitest, parseCLI } = await import(pathToFileURL(require.resolve('vitest/node')).href)
  const major = Number(require('vitest/package.json').version.split('.')[0])
  // Use Vitest's own parser to preserve command options. Positional filters
  // are replaced by the exact assignment.
  const { options } = parseCLI(['vitest', ...process.argv.slice(2)])
  if (options.exclude) {
    options.cliExclude = options.exclude
    delete options.exclude
  }
  // startVitest performs package validation and starts the Datadog session.
  // Standalone watch mode keeps its context open for the explicit API run;
  // disable Vite's watcher so this remains a single ddtest worker invocation.
  const config = {
    ...options, run: false, watch: true, standalone: true,
  }
  const overrides = { server: { watch: null } }
  const context = major >= 5
    ? await startVitest([], config, overrides)
    : await startVitest('test', [], config, overrides)
  if (!context) {
    throw new Error('Vitest could not initialize the exact-file runner')
  }
  try {
    if (process.exitCode) {
      throw new Error('Vitest failed to initialize the exact-file runner')
    }
    const modern = major >= 3
    // Vitest 3+ has a public specification API. Vitest 1-2 use the older
    // discovery/run methods; keep the original specs (including project/pool).
    let specs = modern
      ? await context.getRelevantTestSpecifications()
      : await context.filterTestsBySource(await context.globTestFiles())
    specs = specs.filter(spec => selected.has(canonicalPath(spec.moduleId ?? spec[1])))
    if (specs.length === 0) {
      console.error('No assigned Vitest test files found')
      process.exitCode = context.config.passWithNoTests ? 0 : 1
    } else if (modern) {
      await context.runTestSpecifications(specs, true)
    } else {
      await context.runFiles(specs, true)
    }
  } catch (error) {
    console.error(error)
    process.exitCode = 1
  } finally {
    await context.exit()
  }
  // The API run replaces the CLI entrypoint. Flush reporters before exiting so
  // the original CLI cannot start a second run with substring filters.
  await Promise.all([process.stdout, process.stderr].map(stream =>
    new Promise(resolve => stream.write('', resolve))))
  process.exit()
}
