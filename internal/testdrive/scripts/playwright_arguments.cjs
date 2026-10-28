const fs = require('node:fs')
const path = require('node:path')
const { createRequire } = require('node:module')

// Normalize the actual Playwright CLI, after package aliases and lifecycle
// hooks have expanded. Never rewrite npm's or a setup script's arguments.
if (process.argv[1] && process.env.DDTEST_PLAYWRIGHT_PROBE) {
  const resolve = createRequire(path.resolve(process.cwd(), 'package.json'))
  const cli = (() => {
    try { return fs.realpathSync(process.argv[1]) }
    catch { return '' } // e.g. node - used by a package lifecycle hook.
  })()
  const matches = ['@playwright/test', 'playwright'].some(name => {
    try { return cli === fs.realpathSync(path.join(path.dirname(resolve.resolve(name + '/package.json')), 'cli.js')) }
    catch { return false }
  })
  if (matches && process.argv[2] === 'test') {
    const { program } = createRequire(cli)('playwright/lib/program')
    const command = program.commands.find(command => command.name() === 'test')
    if (!command) throw new Error('ddtest could not inspect Playwright CLI options')
    const replace = new Set(['--project', '--shard', '--repeat-each', '--retries', '--grep', '-g', '--grep-invert', '-G', '--test-list', '--test-list-invert', '--last-failed', '--only-changed', '--ui', '--ui-host', '--ui-port', '--debug'])
    const args = process.argv.slice(3), keep = []
    for (let i = 0; i < args.length;) {
      const token = args[i], name = token.split('=')[0]
      if (token === '--') break // Remaining operands are original file filters.
      if (!token.startsWith('-')) { i++; continue }
      const option = command.options.find(option => option.is(name))
      if (!option) throw new Error('ddtest cannot safely normalize Playwright option: ' + token)
      let end = i + 1
      if (!token.includes('=')) {
        if (option.required && !option.variadic) end++
        else if (option.optional || option.variadic) {
          while (end < args.length && !args[end].startsWith('-')) {
            end++
            if (!option.variadic) break
          }
        }
      }
      if (!replace.has(name)) keep.push(...args.slice(i, end))
      i = end
    }
    const file = '^' + process.env.DDTEST_PLAYWRIGHT_PROBE.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$'
    process.argv = [process.argv[0], process.argv[1], 'test', file, ...keep,
      '--project=' + process.env.DDTEST_PLAYWRIGHT_PROJECT,
      '--repeat-each=1', '--retries=0', '--no-deps', '--grep=ddtest validation probe$']
    fs.writeFileSync(process.env.DDTEST_PLAYWRIGHT_ARGUMENTS_REPORT, JSON.stringify(process.argv))
  }
}
