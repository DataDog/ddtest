'use strict'

// Loaded before the tracer preload. A Mocha run that the validated suite
// starts itself, such as a fixture run, would report to the same local intake;
// keep its telemetry out of the comparison, as the observer keeps its results.
const path = require('node:path')
const owner = process.env.DDTEST_MOCHA_OWNER
const entry = process.argv[1] || ''
if (owner && owner !== String(process.pid)) {
  if (['mocha', 'mocha.js', '_mocha'].includes(path.basename(entry))) {
    process.env.DD_CIVISIBILITY_ENABLED = 'false'
    process.env.DD_TRACE_ENABLED = 'false'
  } else if (!/[\\/]mocha[\\/]lib[\\/]nodejs[\\/]worker\.c?js$/.test(entry) || String(process.ppid) !== owner) {
    // Only the owner's parallel workers report the suite. Any other process,
    // such as a script running Mocha programmatically or that script's own
    // workers, keeps tracing everything except Mocha.
    for (const name of ['DD_TRACE_DISABLED_PLUGINS', 'DD_TRACE_DISABLED_INSTRUMENTATIONS']) {
      process.env[name] = [process.env[name], 'mocha'].filter(Boolean).join(',')
    }
  }
}
