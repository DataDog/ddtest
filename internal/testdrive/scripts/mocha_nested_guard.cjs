'use strict'

// Loaded before the tracer preload. A Mocha CLI that the validated suite
// starts itself, such as a fixture run, would report to the same local intake;
// keep its telemetry out of the comparison. Parallel workers are not CLIs.
const path = require('node:path')
const owner = process.env.DDTEST_MOCHA_OWNER
const entry = process.argv[1] ? path.basename(process.argv[1]) : ''
if (owner && owner !== String(process.pid) && ['mocha', 'mocha.js', '_mocha'].includes(entry)) {
  process.env.DD_CIVISIBILITY_ENABLED = 'false'
  process.env.DD_TRACE_ENABLED = 'false'
}
