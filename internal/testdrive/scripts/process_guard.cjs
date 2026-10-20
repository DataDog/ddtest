const fs = require('node:fs')
const ownProcess = require('./process_owner.cjs')

// The launcher execs the original command, preserving its PID, parent, pipes
// and exit behavior. This live member anchors that same detached group.
const group = Number(process.argv[2])
process.on('SIGTERM', () => {})
ownProcess(signal => process.kill(-group, signal), true, () => fs.writeFileSync(process.argv[3], 'ready'))
