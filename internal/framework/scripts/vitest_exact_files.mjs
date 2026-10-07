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
  const { createVitest, parseCLI } = await import(pathToFileURL(require.resolve('vitest/node')).href)
  const major = Number(require('vitest/package.json').version.split('.')[0])
  const runner = major >= 3
    ? await import('./vitest_exact_files_modern.mjs')
    : await import('./vitest_exact_files_legacy.mjs')
  // Use Vitest's own parser to preserve command options. Positional filters
  // are replaced by the exact assignment.
  const { options } = parseCLI(['vitest', ...process.argv.slice(2)])
  if (options.exclude) {
    options.cliExclude = options.exclude
    delete options.exclude
  }
  // Match the CLI's environment before loading the user's configuration.
  process.env.TEST = 'true'
  process.env.VITEST = 'true'
  process.env.NODE_ENV ??= 'test'

  // Own the lifecycle so discovery cannot execute files before we filter them.
  // dd-trace 5.125+ instruments createVitest directly; no watch mode is needed.
  const config = { ...options, run: true, watch: false }
  const context = major >= 5
    ? await createVitest(config)
    : await createVitest('test', config)
  try {
    // Initialize reporters and coverage without starting a test run.
    await runner.initialize(context)
    const discovered = await runner.discoverSpecifications(context)
    const specs = discovered.filter(spec => selected.has(canonicalPath(runner.filePath(spec))))
    if (specs.length === 0) {
      console.error('No assigned Vitest test files found')
      process.exitCode = context.config.passWithNoTests ? 0 : 1
    } else {
      await runner.runSpecifications(context, specs)
    }
  } catch (error) {
    console.error(error)
    process.exitCode = 1
  } finally {
    await context.close()
  }
  // The API run replaces the CLI entrypoint. Flush reporters before exiting so
  // the original CLI cannot start a second run with substring filters.
  await Promise.all([process.stdout, process.stderr].map(stream =>
    new Promise(resolve => stream.write('', resolve))))
  process.exit()
}
