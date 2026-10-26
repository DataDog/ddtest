const fs = require('node:fs')
const path = require('node:path')
const ownProcess = require('./process_owner.cjs')
const { spawnSync } = require('node:child_process')
// Browser relaunches must release their guards promptly, without busy polling.
const idlePollMilliseconds = 150

// The launcher execs the original command, preserving its PID, parent, pipes
// and exit behavior. This live member anchors that same detached group.
const group = Number(process.argv[2])
// Keepers record themselves per group so launchers reuse one and
// keepers never count one another as live members.
const keepers = process.argv[4]
const self = keepers && path.join(keepers, String(process.pid))
const othersLive = () => {
  const snapshot = spawnSync('/bin/ps', ['-axo', 'pgid=,pid=,stat='], { encoding: 'utf8' })
  if (snapshot.error || snapshot.status !== 0) return true
  let peers = []
  try { peers = keepers ? fs.readdirSync(keepers).map(Number) : [] } catch {}
  return snapshot.stdout.trim().split('\n').some(line => {
    const [pgid, pid, state] = line.trim().split(/\s+/)
    return Number(pgid) === group && Number(pid) !== process.pid && Number(pid) !== snapshot.pid && !state.startsWith('Z') && !peers.includes(Number(pid))
  })
}
process.on('SIGTERM', () => {})
ownProcess(signal => process.kill(-group, signal), true, () => {
  if (self) {
    fs.mkdirSync(keepers, { recursive: true })
    fs.writeFileSync(self, '')
  }
  fs.writeFileSync(process.argv[3], 'ready')
})
setInterval(() => {
  if (othersLive()) return
  if (self) {
    // Withdraw first, then confirm no launcher joined the group meanwhile.
    fs.rmSync(self, { force: true })
    if (othersLive()) {
      fs.writeFileSync(self, '')
      return
    }
  }
  process.exit(0)
}, idlePollMilliseconds)
