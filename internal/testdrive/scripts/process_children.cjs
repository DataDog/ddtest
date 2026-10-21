const childProcess = require('node:child_process')
const spawn = childProcess.ChildProcess.prototype.spawn
const fs = require('node:fs')
const path = require('node:path')
// A supervisor limitation must never be mistaken for a customer test failure.
const unsupportedExit = 86
// Five seconds bounds owner registration before the target is allowed to start.
const readinessAttempts = 500
const readinessInterval = 0.01

function executableExists (options, env) {
  const cwd = options.cwd ? String(options.cwd) : process.cwd()
  try {
    if (!fs.statSync(cwd).isDirectory()) return false
    const candidates = options.file.includes('/') ? [path.resolve(cwd, options.file)]
      : (env.PATH ?? '/usr/bin:/bin').split(':').map(entry => path.resolve(cwd, entry, options.file))
    return candidates.some(file => {
      try { fs.accessSync(file, fs.constants.X_OK); return fs.statSync(file).isFile() }
      catch { return false }
    })
  } catch { return false }
}

if (process.env.DDTEST_PROCESS_OWNER && !process.env.DDTEST_PROCESS_GUARD) {
  require('./process_owner.cjs')(signal => process.kill(process.pid, signal))
  // All asynchronous launch APIs (including fork and execFile) reach this hook.
  childProcess.ChildProcess.prototype.spawn = function (options) {
    const args = options.args || []
    const env = Object.fromEntries((options.envPairs || []).map(pair => {
      const index = pair.indexOf('=')
      return [pair.slice(0, index), pair.slice(index + 1)]
    }))
    // Delegate launch failures to Node itself, preserving native error events
    // (ENOENT/EACCES) rather than turning them into shell exit codes 127/126.
    if (!executableExists(options, env)) return Reflect.apply(spawn, this, [options])
    if (!fs.existsSync(process.env.DDTEST_PROCESS_OWNER) || !fs.existsSync(__filename)) {
      env.NODE_OPTIONS = (env.NODE_OPTIONS || '').replace(/(?:^|\s)(?:--require(?:=|\s+)|-r\s+)(?:"([^"]+)"|'([^']+)'|([^\s]+))/g, (option, quoted, single, plain) => {
        const file = quoted || single || plain
        try { return path.join(fs.realpathSync(path.dirname(file)), path.basename(file)) === __filename ? '' : option }
        catch { return path.resolve(file) === __filename ? '' : option }
      }).trim()
      delete env.DDTEST_PROCESS_OWNER
      delete env.DDTEST_PROCESS_ERRORS
      return Reflect.apply(spawn, this, [{ ...options, envPairs: Object.entries(env).map(([key, value]) => key + '=' + value) }])
    }
    env.DDTEST_PROCESS_OWNER = process.env.DDTEST_PROCESS_OWNER
    env.DDTEST_PROCESS_ERRORS = process.env.DDTEST_PROCESS_ERRORS
    // Preserve the child's own options, adding ownership even when a custom
    // environment omitted the parent's preloads. Node grandchildren can then
    // supervise their own detached launches too.
    if (!(env.NODE_OPTIONS || '').includes(__filename)) {
      env.NODE_OPTIONS = (env.NODE_OPTIONS || '') + ' --require ' + JSON.stringify(__filename)
    }
    const stdio = options.stdio ?? 'pipe'
    if (Array.isArray(stdio) && stdio.includes('ipc')) {
      if (options.file !== process.execPath) {
        require('./process_owner.cjs').recordFailure('non-Node IPC child cannot be supervised', 'unsupported_detached_spawn')
        process.exit(unsupportedExit)
      }
      // A fork with a custom environment may omit NODE_OPTIONS. Add only the
      // supervisor; do not restore the parent's tracer or application preloads.
      return Reflect.apply(spawn, this, [{ ...options, envPairs: Object.entries(env).map(([key, value]) => key + '=' + value) }])
    }
    if (process.platform === 'win32' || (args.length && args[0] !== options.file)) {
      require('./process_owner.cjs').recordFailure('unsupported supervised spawn configuration', 'unsupported_detached_spawn')
      // Do not launch a child that cannot be owned, or let matching supervisor
      // failures in both modes masquerade as an existing customer test failure.
      process.exit(unsupportedExit)
    }
    // Close browser protocol pipes only in the keeper, before Node starts.
    // The original executable inherits the untouched descriptors via exec.
    const closeExtra = Array.isArray(stdio) ? stdio.slice(3).map((_, i) => `${i + 3}<&-`).join(' ') : ''
    const launcher = `keeper_node=$1; keeper_script=$2; shift 2
ready=$(mktemp "\${DDTEST_PROCESS_OWNER%/*}/ready.XXXXXXXX") || exit 1
NODE_OPTIONS= DD_TRACE_ENABLED=false DD_CIVISIBILITY_ENABLED=false "$keeper_node" "$keeper_script" "$$" "$ready" </dev/null >/dev/null 2>&1 ${closeExtra} &
keeper=$!; attempts=0
while [ ! -s "$ready" ]; do
  kill -0 "$keeper" 2>/dev/null || exit 1
  attempts=$((attempts + 1)); [ "$attempts" -lt ${readinessAttempts} ] || exit 1
  sleep ${readinessInterval}
done
rm -f "$ready"
unset DDTEST_PROCESS_GUARD
exec "$@"`
    env.DDTEST_PROCESS_GUARD = '1'
    // Bash closes descriptors above 9; -p ignores ambient BASH_ENV/functions
    // in this helper shell while leaving the target's environment untouched.
    return Reflect.apply(spawn, this, [{ ...options, file: '/bin/bash', detached: true,
      args: ['/bin/bash', '-p', '-c', launcher, 'ddtest-owned-launcher', process.execPath, require.resolve('./process_guard.cjs'), options.file, ...args.slice(1)],
      envPairs: Object.entries(env).map(([key, value]) => key + '=' + value) }])
  }
}
