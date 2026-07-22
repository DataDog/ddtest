import { createVitest, parseCLI } from 'vitest/node'

const outputMarker = '__DDTEST_VITEST_FILES__'
const cliArgs = JSON.parse(process.argv[1])
const { filter, options } = parseCLI(cliArgs)
const vitest = await createVitest('test', { ...options, watch: false })

try {
  const specs = await vitest.globTestFiles(filter)
  const files = specs.map(spec => Array.isArray(spec) ? spec[1] : spec)
  process.stdout.write(`${outputMarker}${JSON.stringify(files)}\n`)
} finally {
  await vitest.close()
}
