const childProcess = require('node:child_process')
const spawn = childProcess.spawn

if (process.env.DDTEST_PROCESS_OWNER && !process.env.DDTEST_PROCESS_GUARD) {
  require('./process_owner.cjs')(signal => process.kill(process.pid, signal))
  childProcess.spawn = function (command, args, options) {
    if (!Array.isArray(args)) { options = args; args = [] }
    if (!options?.detached) return Reflect.apply(spawn, this, [command, args, options])
    const stdio = options.stdio ?? 'pipe'
    if (process.platform === 'win32' || options.argv0 || (Array.isArray(stdio) && (stdio.includes('ipc') || stdio.length > 10))) {
      require('./process_owner.cjs').recordFailure('unsupported detached spawn configuration', 'unsupported_detached_spawn')
      // Do not launch a child that cannot be owned, or let matching supervisor
      // failures in both modes masquerade as an existing customer test failure.
      process.exit(86)
    }
    // Close browser protocol pipes only in the keeper, before Node starts.
    // The original executable inherits the untouched descriptors via exec.
    const closeExtra = Array.isArray(stdio) ? stdio.slice(3).map((_, i) => `${i + 3}<&-`).join(' ') : ''
    const launcher = `keeper_node=$1; keeper_script=$2; shift 2
ready=$(mktemp "\${DDTEST_PROCESS_OWNER%/*}/ready.XXXXXXXX") || exit 1
"$keeper_node" "$keeper_script" "$$" "$ready" </dev/null >/dev/null 2>&1 ${closeExtra} &
keeper=$!; attempts=0
while [ ! -s "$ready" ]; do
  kill -0 "$keeper" 2>/dev/null || exit 1
  attempts=$((attempts + 1)); [ "$attempts" -lt 500 ] || exit 1
  sleep 0.01
done
rm -f "$ready"
unset DDTEST_PROCESS_GUARD
exec "$@"`
    const invocation = options.shell ? [options.shell === true ? '/bin/sh' : options.shell, '-c', [command, ...args].join(' ')] : [command, ...args]
    const env = { ...(options.env ?? process.env), DDTEST_PROCESS_OWNER: process.env.DDTEST_PROCESS_OWNER, DDTEST_PROCESS_GUARD: '1', DDTEST_PROCESS_ERRORS: process.env.DDTEST_PROCESS_ERRORS }
    return Reflect.apply(spawn, this, ['/bin/sh', ['-c', launcher, 'ddtest-owned-launcher', process.execPath, require.resolve('./process_guard.cjs'), ...invocation], { ...options, shell: false, env }])
  }
}
