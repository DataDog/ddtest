import { readFileSync, realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { basename, dirname, resolve } from 'node:path'
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
  const { createVitest } = await import(pathToFileURL(require.resolve('vitest/node')).href)

  // Vitest does not export the project class on every supported version. An
  // empty, config-free context gives us its prototype without loading the
  // user's configuration twice. The original CLI still owns the real run,
  // including reporters, coverage, tracer hooks, exit codes, and teardown.
  const context = await createVitest('test', {
    root: dirname(filesPath), config: false, watch: false, include: [],
  })
  try {
    const project = context.projects[0]
    if (!project || typeof project.filterFiles !== 'function') {
      throw new Error('ddtest cannot enforce exact file selection with this Vitest version')
    }
    const prototype = Object.getPrototypeOf(project)
    prototype.filterFiles = function (files) {
      return files.filter(file => selected.has(canonicalPath(file)))
    }
  } finally {
    await context.close()
  }
}
