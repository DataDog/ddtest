const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');

async function gate() {
  const worker = ({ '0': 'alpha', '1': 'beta' })[process.env.REPRO_WORKER] || process.env.REPRO_WORKER;
  const directory = process.env.REPRO_GATE;
  fs.writeFileSync(path.join(directory, `${worker}.ready`), 'ready');
  const deadline = Date.now() + 90000;
  while (!fs.existsSync(path.join(directory, `${worker}.release`))) {
    if (Date.now() > deadline) throw new Error(`Timed out waiting for worker ${worker} release`);
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  if (process.env.REPRO_FAIL_ALPHA === "1" && worker === "alpha") throw new Error("Intentional alpha test failure");
}

// NYC supports instrumented subprocesses. Persist real coverage while the test
// runner remains alive, then let the second independent NYC invocation start.
async function childCoverage() {
  const worker = ({ '0': 'alpha', '1': 'beta' })[process.env.REPRO_WORKER] || process.env.REPRO_WORKER;
  const child = spawnSync(process.execPath, [path.join(__dirname, 'src', `${worker}.cjs`)], {
    encoding: 'utf8', timeout: 15000,
  });
  assert.equal(child.status, 0, child.stderr || String(child.error || 'child failed'));
  assert.equal(child.stdout.trim(), worker === 'alpha' ? '11' : '22');
  await gate();
}
module.exports = { gate, childCoverage };
