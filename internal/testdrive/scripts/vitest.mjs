import { createRequire } from 'node:module'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

// The entrypoint lives in the testdrive output directory, but Vitest and its
// dependency resolution belong to the customer's project (including PnP).
const require = createRequire(resolve('package.json'))
let packagePath
try {
  packagePath = require.resolve('vitest/package.json')
} catch (cause) {
  throw new Error('Vitest is not resolvable from this project. Install project dependencies and make any project loaders available through NODE_OPTIONS.', { cause })
}
const { bin } = require(packagePath)
const executable = typeof bin === 'string' ? bin : bin?.vitest
if (!executable) throw new Error('The installed Vitest package does not declare a CLI entrypoint.')
const cli = resolve(dirname(packagePath), executable)

// Invoke the installed CLI once, with its normal configuration and lifecycle.
// This full-suite path does not affect DDTest's exact-file execution adapter.
process.argv = [process.execPath, cli, 'run', ...process.argv.slice(2)]
await import(pathToFileURL(cli).href)
