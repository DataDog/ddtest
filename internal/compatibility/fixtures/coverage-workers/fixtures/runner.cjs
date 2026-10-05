const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const entries = { jest: 'jest/bin/jest.js', vitest: 'vitest/vitest.mjs', mocha: 'mocha/bin/mocha.js',
  cucumber: '@cucumber/cucumber/bin/cucumber.js', playwright: '@playwright/test/cli.js', cypress: 'cypress/bin/cypress' };
const framework = process.argv[2] === 'cucumber-js' ? 'cucumber' : process.argv[2];
const entry = entries[framework];
const packageName = entry.startsWith('@') ? entry.split('/').slice(0,2).join('/') : entry.split('/')[0];
const executable = path.join(path.dirname(require.resolve(packageName+'/package.json')),entry.slice(packageName.length+1));
const args = [executable, ...process.argv.slice(3)];
const linuxCypress = framework === 'cypress' && process.platform === 'linux';
const child = spawn(linuxCypress ? 'xvfb-run' : process.execPath,
  linuxCypress ? ['-a', process.execPath, ...args] : args, { stdio: 'inherit' });
child.on('error', error => { console.error(error); process.exitCode = 1; });
child.on('exit', (code, signal) => {
  if (process.env.REPRO_WORKER !== undefined && process.env.REPRO_GATE) {
    const worker = process.env.REPRO_WORKER === '0' ? 'alpha' : 'beta';
    if (worker === 'alpha' && code === 0 && process.env.REPRO_REPORT_ERROR) {
      const report = path.join(process.env.DDTEST_WORKER_COVERAGE_DIRECTORY, 'report', 'coverage-final.json');
      if (process.env.REPRO_REPORT_ERROR === 'missing') fs.unlinkSync(report);
      else fs.writeFileSync(report, '{malformed');
    }
    fs.writeFileSync(path.join(process.env.REPRO_GATE, `${worker}.exited`), JSON.stringify({code,signal}));
  }
  process.exitCode = code === null ? 1 : code;
});
