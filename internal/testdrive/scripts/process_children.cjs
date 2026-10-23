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
      try { fs.accessSync(file, fs.constants.X_OK); return fs.statSync(file).isFile() && interpreterExists(file) }
      catch { return false }
    })
  } catch { return false }
}

// A script whose #! interpreter is missing fails with ENOENT natively; the
// supervised exec would instead exit 127 without an error event.
function interpreterExists (file) {
  const header = Buffer.alloc(256)
  const fd = fs.openSync(file, 'r')
  let length
  try { length = fs.readSync(fd, header, 0, header.length, 0) } finally { fs.closeSync(fd) }
  const line = header.subarray(0, length).toString('latin1').split('\n', 1)[0]
  if (!line.startsWith('#!')) return true
  const interpreter = line.slice(2).trim().split(/\s+/, 1)[0]
  if (!interpreter || !interpreter.startsWith('/')) return true
  try { fs.accessSync(interpreter, fs.constants.X_OK); return true } catch { return false }
}

// The helper shell finds its own tools in system locations first, never in the
// child's PATH, which may be restricted or point at customer replacements.
const toolPaths = new Map()
function tool (name) {
  if (!toolPaths.has(name)) {
    const directories = ['/usr/bin', '/bin', ...(process.env.PATH || '').split(':').filter(Boolean)]
    const found = directories.map(directory => path.join(directory, name)).find(file => {
      try { fs.accessSync(file, fs.constants.X_OK); return fs.statSync(file).isFile() } catch { return false }
    })
    toolPaths.set(name, found || name)
  }
  return toolPaths.get(name)
}

// A child whose owner is gone runs natively, without this preload.
function withoutOwnership (env) {
  env.NODE_OPTIONS = (env.NODE_OPTIONS || '').replace(/(?:^|\s)(?:--require(?:=|\s+)|-r\s+)(?:"([^"]+)"|'([^']+)'|([^\s]+))/g, (option, quoted, single, plain) => {
    const file = quoted || single || plain
    try { return path.join(fs.realpathSync(path.dirname(file)), path.basename(file)) === __filename ? '' : option }
    catch { return path.resolve(file) === __filename ? '' : option }
  }).trim()
  delete env.DDTEST_PROCESS_OWNER
  delete env.DDTEST_PROCESS_ERRORS
  return env
}

// Preserve the child's own options, adding ownership even when a custom
// environment omitted the parent's preloads. Node grandchildren can then
// supervise their own detached launches too.
function withOwnership (env) {
  env.DDTEST_PROCESS_OWNER = process.env.DDTEST_PROCESS_OWNER
  env.DDTEST_PROCESS_ERRORS = process.env.DDTEST_PROCESS_ERRORS
  if (!(env.NODE_OPTIONS || '').includes(__filename)) {
    env.NODE_OPTIONS = (env.NODE_OPTIONS || '') + ' --require ' + JSON.stringify(__filename)
  }
  return env
}

function ownerAvailable () {
  return fs.existsSync(process.env.DDTEST_PROCESS_OWNER) && fs.existsSync(__filename)
}

function unsupportedLaunch () {
  require('./process_owner.cjs').recordFailure('unsupported supervised spawn configuration', 'unsupported_detached_spawn')
  // Do not launch a child that cannot be owned, or let matching supervisor
  // failures in both modes masquerade as an existing customer test failure.
  process.exit(unsupportedExit)
}

// The helper shell's arguments: it starts a keeper in the target's new process
// group, then execs the original executable with its own PID, parent and pipes.
function launcherArguments (file, args, stdio) {
  // Close browser protocol pipes only in the keeper, before Node starts.
  // The original executable inherits the untouched descriptors via exec.
  const closeExtra = Array.isArray(stdio) ? stdio.slice(3).map((_, i) => `${i + 3}<&-`).join(' ') : ''
  const launcher = `keeper_node=$1; keeper_script=$2; mktemp_tool=$3; sleep_tool=$4; rm_tool=$5; shift 5
ready=$("$mktemp_tool" "\${DDTEST_PROCESS_OWNER%/*}/ready.XXXXXXXX") || exit 1
(unset NODE_CHANNEL_FD NODE_CHANNEL_SERIALIZATION_MODE
NODE_OPTIONS= DD_TRACE_ENABLED=false DD_CIVISIBILITY_ENABLED=false exec "$keeper_node" "$keeper_script" "$$" "$ready") </dev/null >/dev/null 2>&1 ${closeExtra} &
keeper=$!; attempts=0
while [ ! -s "$ready" ]; do
  kill -0 "$keeper" 2>/dev/null || exit 1
  [ -S "$DDTEST_PROCESS_OWNER" ] || exit 1
  attempts=$((attempts + 1)); [ "$attempts" -lt ${readinessAttempts} ] || exit 1
  "$sleep_tool" ${readinessInterval}
done
"$rm_tool" -f "$ready"
unset DDTEST_PROCESS_GUARD
exec "$@"`
  // Bash closes descriptors above 9; -p ignores ambient BASH_ENV/functions
  // in this helper shell while leaving the target's environment untouched.
  return ['-p', '-c', launcher, 'ddtest-owned-launcher', process.execPath, require.resolve('./process_guard.cjs'), tool('mktemp'), tool('sleep'), tool('rm'), file, ...args]
}

const envPairs = env => Object.entries(env).map(([key, value]) => key + '=' + value)

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
    if (!ownerAvailable()) return Reflect.apply(spawn, this, [{ ...options, envPairs: envPairs(withoutOwnership(env)) }])
    withOwnership(env)
    const stdio = options.stdio ?? 'pipe'
    if (Array.isArray(stdio) && stdio.includes('ipc') && options.file === process.execPath) {
      // Native Node forks inherit their own ownership preload and IPC channel.
      return Reflect.apply(spawn, this, [{ ...options, envPairs: envPairs(env) }])
    }
    if (process.platform === 'win32' || (args.length && args[0] !== options.file)) unsupportedLaunch()
    env.DDTEST_PROCESS_GUARD = '1'
    return Reflect.apply(spawn, this, [{ ...options, file: '/bin/bash', detached: true,
      args: ['/bin/bash', ...launcherArguments(options.file, args.slice(1), stdio)], envPairs: envPairs(env) }])
  }

  // spawnSync, execSync and execFileSync never reach the asynchronous hook, and
  // the exec variants call Node's internal spawnSync, so each is wrapped. A
  // supervised launch goes through the same owned launcher; its exit status,
  // output, timeout and errors stay those of the original executable.
  const native = { spawnSync: childProcess.spawnSync, execSync: childProcess.execSync, execFileSync: childProcess.execFileSync }
  const optionalArguments = (args, options) => Array.isArray(args) ? [args, options] : [[], args]
  const shellCommand = (file, args, shell) => ({ file: typeof shell === 'string' ? shell : '/bin/sh', args: ['-c', [file, ...args].join(' ')] })
  // Returns undefined to run the original call unchanged, {env} to run it with
  // that environment, or the owned launch to run instead.
  const supervisedSync = (file, args, options) => {
    options = { ...options }
    if (options.shell) {
      ({ file, args } = shellCommand(file, args, options.shell))
      delete options.shell
    }
    const env = Object.fromEntries(Object.entries(options.env ?? process.env).filter(([, value]) => value !== undefined).map(([key, value]) => [key, String(value)]))
    if (typeof file !== 'string' || !executableExists({ file, cwd: options.cwd }, env)) return undefined
    if (!ownerAvailable()) return { env: withoutOwnership(env) }
    if (process.platform === 'win32' || (options.argv0 !== undefined && options.argv0 !== file)) unsupportedLaunch()
    withOwnership(env).DDTEST_PROCESS_GUARD = '1'
    return { file: '/bin/bash', args: launcherArguments(file, args, options.stdio), options: { ...options, env, detached: true }, original: { file, args } }
  }
  // Errors name the original executable and command, never the helper shell.
  const restoreErrorSubject = (error, launch, command) => {
    if (!error) return error
    const helper = [launch.file, ...launch.args].join(' ')
    if (typeof error.message === 'string') {
      error.message = error.message.replace('Command failed: ' + helper, 'Command failed: ' + (command ?? [launch.original.file, ...launch.original.args].join(' ')))
        .replace('spawnSync ' + launch.file, 'spawnSync ' + launch.original.file)
    }
    if (error.path === launch.file) error.path = launch.original.file
    if (Array.isArray(error.spawnargs)) error.spawnargs = launch.original.args
    return error
  }
  // Each wrapper keeps Node's own argument handling for calls it doesn't change.
  childProcess.spawnSync = function (...call) {
    const [file, args, options] = [call[0], ...optionalArguments(call[1], call[2])]
    const launch = supervisedSync(file, args || [], options)
    if (!launch) return Reflect.apply(native.spawnSync, this, call)
    if (!launch.file) return native.spawnSync(file, args || [], { ...options, env: launch.env })
    const result = native.spawnSync(launch.file, launch.args, launch.options)
    restoreErrorSubject(result.error, launch)
    return result
  }
  childProcess.execFileSync = function (...call) {
    const [file, args, options] = [call[0], ...optionalArguments(call[1], call[2])]
    const launch = supervisedSync(file, args || [], options)
    if (!launch) return Reflect.apply(native.execFileSync, this, call)
    if (!launch.file) return native.execFileSync(file, args || [], { ...options, env: launch.env })
    try { return native.execFileSync(launch.file, launch.args, launch.options) } catch (error) { throw restoreErrorSubject(error, launch) }
  }
  childProcess.execSync = function (...call) {
    const [command, options] = call
    const { file, args } = shellCommand(command, [], options?.shell ?? true)
    const launch = supervisedSync(file, args, { ...options, shell: undefined })
    if (!launch) return Reflect.apply(native.execSync, this, call)
    if (!launch.file) return native.execSync(command, { ...options, env: launch.env })
    try { return native.execFileSync(launch.file, launch.args, launch.options) } catch (error) { throw restoreErrorSubject(error, launch, command) }
  }
}
