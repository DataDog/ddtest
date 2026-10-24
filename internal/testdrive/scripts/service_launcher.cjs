const childProcess = require('node:child_process')
const path = require('node:path')
const ownProcess = require('./process_owner.cjs')

// The supervisor itself starts without customer preloads. Restore them only for
// the target, with ownership first, after its owner connection is established.
const nodeOptions = process.env.DDTEST_SERVICE_NODE_OPTIONS || ''
delete process.env.DDTEST_SERVICE_NODE_OPTIONS
process.env.NODE_OPTIONS = '--require ' + JSON.stringify(path.join(__dirname, 'children.cjs')) + ' ' + nodeOptions
require('./children.cjs')
ownProcess(signal => process.kill(process.pid, signal), true, () => {
  const child = childProcess.spawn(process.argv[2], process.argv.slice(3), { stdio: 'inherit' })
  child.on('error', error => { console.error(error.message); process.exit(1) })
  child.on('exit', code => process.exit(code === null ? 1 : code))
})
