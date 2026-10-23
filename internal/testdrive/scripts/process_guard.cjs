const fs = require('node:fs')
const ownProcess = require('./process_owner.cjs')
const { spawnSync } = require('node:child_process')
// Browser relaunches must release their guards promptly, without busy polling.
const idlePollMilliseconds = 150

// The launcher execs the original command, preserving its PID, parent, pipes
// and exit behavior. This live member anchors that same detached group.
const group = Number(process.argv[2])
process.on('SIGTERM', () => {})
ownProcess(signal => process.kill(-group, signal), true, () => fs.writeFileSync(process.argv[3], 'ready'))
setInterval(() => {
  const snapshot = spawnSync('/bin/ps', ['-axo', 'pgid=,pid=,stat='], { encoding: 'utf8' })
  if (snapshot.error || snapshot.status !== 0) return
  const live = snapshot.stdout.trim().split('\n').some(line => {
    const [pgid, pid, state] = line.trim().split(/\s+/)
    return Number(pgid) === group && Number(pid) !== process.pid && Number(pid) !== snapshot.pid && !state.startsWith('Z')
  })
  if (!live) process.exit(0)
}, idlePollMilliseconds)
