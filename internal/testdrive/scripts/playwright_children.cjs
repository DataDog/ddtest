const fs = require('node:fs')
const childProcess = require('node:child_process')
const spawn = childProcess.spawn
if (process.env.DDTEST_PLAYWRIGHT_CHILDREN) {
  fs.appendFileSync(process.env.DDTEST_PLAYWRIGHT_CHILDREN, String(process.pid) + '\n')
}

// Record Node descendants and detached webServer groups from this invocation.
// Wrappers such as Turbo can outlive their parent group. Reused customer servers
// never inherit this preload and are not recorded or stopped.
childProcess.spawn = function (...args) {
  const child = Reflect.apply(spawn, this, args)
  const options = Array.isArray(args[1]) ? args[2] : args[1]
  if (options && options.detached && child.pid && process.env.DDTEST_PLAYWRIGHT_CHILDREN) {
    fs.appendFileSync(process.env.DDTEST_PLAYWRIGHT_CHILDREN, String(-child.pid) + '\n')
  }
  return child
}
