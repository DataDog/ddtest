#!/usr/bin/env node
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');
const { createCoverageMap } = require('istanbul-lib-coverage');

const root = __dirname;
const frameworks = ['jest', 'mocha', 'cypress', 'playwright', 'cucumber', 'vitest'];
const nycFrameworks = new Set(['mocha', 'playwright', 'cucumber']);
const args = process.argv.slice(2);
function option(name, fallback) {
  const index = args.indexOf(name);
  if (index < 0) return fallback;
  if (!args[index + 1] || args[index + 1].startsWith('--')) throw new Error(`Missing value for ${name}`);
  return args[index + 1];
}
if (args.includes('--help')) {
  console.log('node run.cjs [--framework all|jest,mocha,cypress,playwright,cucumber,vitest] [--repeat N] [--ddtest /path/to/ddtest] [--execution parallel|ci-node] [--fail-worker alpha | --report-error missing|malformed]');
  process.exit(0);
}
for (let i = 0; i < args.length; i += 2) {
  if (!['--framework', '--repeat', '--ddtest', '--execution', '--fail-worker', '--report-error'].includes(args[i])) throw new Error(`Unknown option ${args[i]}`);
}
const requested = option('--framework', 'all');
const selected = requested === 'all' ? frameworks : requested.split(',');
if (selected.some(f => !frameworks.includes(f))) throw new Error('Unknown framework');
const ddtest = option('--ddtest', '');
const failWorker = option('--fail-worker', '');
const reportError = option('--report-error', '');
if (failWorker && failWorker !== 'alpha') throw new Error('--fail-worker must be alpha');
if (reportError && !['missing','malformed'].includes(reportError)) throw new Error('--report-error must be missing or malformed');
if ((failWorker || reportError) && !ddtest) throw new Error('Failure checks require --ddtest');
if (failWorker && reportError) throw new Error('Run failure checks separately');
const execution = option('--execution', 'parallel');
if (!['parallel', 'ci-node'].includes(execution)) throw new Error('Unknown execution mode');
const repeat = Number(option('--repeat', '1'));
if (!Number.isSafeInteger(repeat) || repeat < 1) throw new Error('--repeat must be a positive integer');
const runRoot = path.join(root, '.runs', `${new Date().toISOString().replace(/[:.]/g, '-')}-${process.pid}`);
fs.mkdirSync(runRoot, { recursive: true });
const active = new Set();
let interrupted = false;

function kill(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  try { process.kill(-child.pid, 'SIGTERM'); } catch {}
  const timer = setTimeout(() => { try { process.kill(-child.pid, 'SIGKILL'); } catch {} }, 2000);
  timer.unref();
}
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  interrupted = true;
  for (const child of active) kill(child);
  process.exitCode = signal === 'SIGINT' ? 130 : 143;
  setTimeout(() => process.exit(process.exitCode), 2500);
});
function cleanEnv() {
  return Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    !/^(DD_|DATADOG_|NYC_|REPRO_|NODE_V8_COVERAGE$|NODE_OPTIONS$|ELECTRON_RUN_AS_NODE$)/.test(key)));
}
function bin(packageName, entry) { return path.join(root, 'node_modules', packageName, entry); }
function command(framework, worker, mode) {
  let runner;
  switch (framework) {
    case 'jest': runner = [bin('jest', 'bin/jest.js'), '--config', 'jest/jest.config.cjs', '--runInBand', `jest/${worker}.test.cjs`]; break;
    case 'vitest': runner = [bin('vitest', 'vitest.mjs'), 'run', '--config', 'vitest/vitest.config.mjs', `vitest/${worker}.test.mjs`]; break;
    case 'mocha': runner = [bin('mocha', 'bin/mocha.js'), 'mocha/worker.test.cjs']; break;
    case 'cucumber': runner = [bin('@cucumber/cucumber', 'bin/cucumber.js'), '--require', 'cucumber/steps.cjs', 'cucumber/worker.feature']; break;
    case 'playwright': runner = [bin('@playwright/test', 'cli.js'), 'test', '--config', 'playwright/playwright.config.cjs']; break;
    case 'cypress': runner = [bin('cypress', 'bin/cypress'), 'run', '--config-file', 'cypress.config.cjs', '--browser', 'electron', '--spec', `cypress/${worker}.cy.cjs`]; break;
  }
  if (nycFrameworks.has(framework)) {
    runner = [bin('nyc', 'bin/nyc.js'), ...(mode === 'shared-no-clean' ? ['--no-clean'] : []), process.execPath, ...runner];
  }
  if (framework === 'cypress' && process.platform === 'linux') {
    return ['xvfb-run', ['-a', process.execPath, ...runner]];
  }
  return [process.execPath, runner];
}
function coveredFiles(map) {
  return ['alpha', 'beta'].filter(name => Object.entries(map).some(([file, coverage]) =>
    file.endsWith(`/${name}.cjs`) && Object.values(coverage.s || {}).some(count => count > 0)));
}
function rawSnapshot(directory) {
  const map = createCoverageMap({});
  if (!fs.existsSync(directory)) return { files: [], covered: [] };
  const files = fs.readdirSync(directory).filter(f => f.endsWith('.json'));
  for (const file of files) map.merge(JSON.parse(fs.readFileSync(path.join(directory, file), 'utf8')));
  return { files, covered: coveredFiles(map.toJSON()) };
}
function start(framework, worker, mode, directory) {
  const gate = path.join(directory, 'gate');
  const temp = path.join(directory, '.nyc_output', mode === 'isolated' || mode === 'temp-only' ? worker : '');
  const report = path.join(directory, 'coverage', mode === 'isolated' ? worker : '');
  const env = { ...cleanEnv(), CI: 'true', REPRO_WORKER: worker, REPRO_GATE: gate,
    REPRO_TEMP: temp, REPRO_REPORT: report, REPRO_TEST_OUTPUT: path.join(directory, 'test-output', worker) };
  const [executable, argv] = command(framework, worker, mode);
  const fd = fs.openSync(path.join(directory, `${worker}.log`), 'w');
  const child = spawn(executable, argv, { cwd: directory, env, detached: true, stdio: ['ignore', fd, fd] });
  fs.closeSync(fd);
  active.add(child);
  const record = { worker, command: [executable, ...argv], temp, report, exitCode: null };
  const done = new Promise(resolve => {
    child.on('error', error => { record.error = error.message; active.delete(child); resolve(); });
    child.on('exit', (code, signal) => { record.exitCode = code; record.signal = signal; active.delete(child); resolve(); });
  });
  return { child, record, done, finished: () => child.exitCode !== null || child.signalCode !== null || record.error };
}
async function until(condition, description, workers, timeout = 120000) {
  const deadline = Date.now() + timeout;
  while (!condition()) {
    if (interrupted) throw new Error('Run interrupted');
    const failed = workers.find(w => w.finished());
    if (failed) throw new Error(`${failed.record.worker} exited before ${description}; see its log`);
    if (Date.now() > deadline) throw new Error(`Timeout: ${description}`);
    await new Promise(resolve => setTimeout(resolve, 50));
  }
}
async function finish(worker, allowFailure = false) {
  await until(() => worker.finished(), `${worker.record.worker} exit`, [], 120000);
  await worker.done;
  if (worker.record.exitCode !== 0 && !allowFailure) throw new Error(`${worker.record.worker} failed; see its log`);
}
async function runCase(framework, mode, iteration) {
  const directory = path.join(runRoot, `${iteration}-${framework}-${mode}`);
  fs.cpSync(path.join(root, 'fixtures'), directory, { recursive: true });
  fs.mkdirSync(path.join(directory, 'gate'));
  fs.writeFileSync(path.join(directory, 'package.json'), JSON.stringify({ name: 'coverage-worker-fixture', private: true }));
  const result = { framework, mode, iteration, directory, workers: [] };
  const workers = [];
  try {
    if (framework === 'cypress') {
      const instrument = spawnSync(process.execPath, [bin('nyc', 'bin/nyc.js'), 'instrument', 'src', 'instrumented'], {
        cwd: directory, encoding: 'utf8', timeout: 30000,
        env: { ...cleanEnv(), REPRO_TEMP: path.join(directory, '.nyc_output'), REPRO_REPORT: path.join(directory, 'coverage') },
      });
      if (instrument.status !== 0) throw new Error(`Instrumentation failed: ${instrument.stderr || instrument.error}`);
    }
    const alpha = start(framework, 'alpha', mode, directory); workers.push(alpha);
    await until(() => fs.existsSync(path.join(directory, 'gate/alpha.ready')), 'alpha ready', workers);
    if (nycFrameworks.has(framework)) {
      result.rawBeforeBeta = rawSnapshot(alpha.record.temp);
      if (!result.rawBeforeBeta.covered.includes('alpha')) throw new Error('Alpha subprocess did not persist coverage');
    }
    const beta = start(framework, 'beta', mode, directory); workers.push(beta);
    await until(() => fs.existsSync(path.join(directory, 'gate/beta.ready')), 'beta ready', workers);
    if (nycFrameworks.has(framework)) {
      result.rawAfterBetaStart = rawSnapshot(alpha.record.temp);
      const alphaPreserved = result.rawAfterBetaStart.covered.includes('alpha');
      if (alphaPreserved !== (mode !== 'shared')) throw new Error('Unexpected NYC cleanup behavior');
    }
    // Both test runners are alive. Finish beta first, so alpha's final report
    // deterministically exposes last-writer loss rather than relying on timing.
    fs.writeFileSync(path.join(directory, 'gate/beta.release'), 'release');
    await finish(beta);
    const betaMap = JSON.parse(fs.readFileSync(path.join(beta.record.report, 'coverage-final.json'), 'utf8'));
    result.betaReportCovered = coveredFiles(betaMap);
    if (!result.betaReportCovered.includes('beta')) throw new Error('Beta did not generate its expected coverage');
    fs.writeFileSync(path.join(directory, 'gate/alpha.release'), 'release');
    await finish(alpha, true);
    if (alpha.record.exitCode !== 0) {
      const log = fs.readFileSync(path.join(directory, 'alpha.log'), 'utf8');
      // Vitest removes its shared .tmp directory during another invocation's
      // cleanup. Accept only this observed coverage failure, never arbitrary
      // test, dependency, browser, or startup failures as a reproduction.
      if (framework === 'vitest' && mode === 'shared' && log.includes('ENOENT') &&
          log.includes(path.join(alpha.record.report, '.tmp', 'coverage-'))) {
        result.coverageError = 'Vitest could not write coverage after its shared .tmp directory was removed';
      } else {
        throw new Error('alpha failed for an unexpected reason; see its log');
      }
    }
    const merged = createCoverageMap({});
    const reportDirs = [...new Set(workers.map(w => w.record.report))];
    for (const reportDir of reportDirs) merged.merge(JSON.parse(fs.readFileSync(path.join(reportDir, 'coverage-final.json'), 'utf8')));
    result.finalCovered = coveredFiles(merged.toJSON());
    fs.writeFileSync(path.join(directory, 'merged-coverage.json'), JSON.stringify(merged.toJSON(), null, 2));
    const control = mode === 'isolated' || mode === 'shared-no-clean';
    if (control && result.finalCovered.length !== 2) throw new Error('Control lost coverage');
    if (!control && result.finalCovered.length === 2) throw new Error('Expected coverage loss was not reproduced');
    if (!control) {
      const expected = framework === 'jest' || framework === 'cypress' ? 'alpha' : 'beta';
      if (result.finalCovered.join(',') !== expected) throw new Error('Unexpected final coverage contents');
    }
    result.status = control ? 'control-passed' : 'reproduced';
  } catch (error) {
    result.status = 'error'; result.error = error.message;
  } finally {
    for (const worker of workers) kill(worker.child);
    await Promise.all(workers.map(w => w.done));
    result.workers = workers.map(w => w.record);
    fs.writeFileSync(path.join(directory, 'result.json'), JSON.stringify(result, null, 2));
  }
  console.log(`${framework.padEnd(10)} ${mode.padEnd(16)} ${result.status}: ${result.error || result.finalCovered.join(', ')}`);
  return result;
}
async function runDDTestCase(framework, iteration, url) {
  const directory = path.join(runRoot, `${iteration}-${framework}-ddtest-${execution}`);
  fs.cpSync(path.join(root, 'fixtures'), directory, { recursive: true });
  fs.mkdirSync(path.join(directory, 'gate'));
  fs.writeFileSync(path.join(directory, 'package.json'), JSON.stringify({ name: 'coverage-worker-fixture', private: true,
    devDependencies: require('./package.json').devDependencies }));
  const result = { framework, execution, iteration, directory };
  let child, done;
  try {
    const specifications = {
      jest: ['jest/*.test.cjs', '--config jest/jest.config.cjs --runInBand'],
      vitest: ['vitest/*.test.mjs', 'run --config vitest/vitest.config.mjs'],
      mocha: ['mocha/*.test.cjs', '--timeout 120000'],
      cucumber: ['cucumber/*.feature', '--require cucumber/steps.cjs cucumber/*.feature'],
      playwright: ['playwright/*.spec.cjs', 'test --config playwright/playwright.config.cjs'],
      cypress: ['cypress/*.cy.cjs', 'run --config-file cypress.config.cjs --browser electron'],
    };
    for (const [folder, original, extension] of [['mocha','worker.test.cjs','test.cjs'],['cucumber','worker.feature','feature'],['playwright','worker.spec.cjs','spec.cjs']]) {
      for (const name of ['alpha','beta']) fs.copyFileSync(path.join(directory,folder,original),path.join(directory,folder,`${name}.${extension}`));
      fs.unlinkSync(path.join(directory,folder,original));
    }
    const playwrightConfig = path.join(directory,'playwright/playwright.config.cjs');
    fs.writeFileSync(playwrightConfig,fs.readFileSync(playwrightConfig,'utf8').replace("'worker.spec.cjs'","'*.spec.cjs'"));
    const env = { ...cleanEnv(), CI:'true', REPRO_GATE:path.join(directory,'gate'),
      REPRO_FAIL_ALPHA:failWorker?'1':'0', REPRO_REPORT_ERROR:reportError,
      REPRO_TEMP:path.join(directory,'.nyc_output'), REPRO_REPORT:path.join(directory,'coverage'),
      REPRO_TEST_OUTPUT:path.join(directory,'test-output'),
      DD_CIVISIBILITY_AGENTLESS_ENABLED:'true', DD_CIVISIBILITY_AGENTLESS_URL:url,
      DD_API_KEY:'local-reproduction-only', DD_TRACE_AGENT_URL:url,
      DD_INSTRUMENTATION_TELEMETRY_ENABLED:'false', DD_CIVISIBILITY_GIT_UPLOAD_ENABLED:'false',
      DD_SERVICE:'coverage-worker-reproduction', DD_ENV:'test',
      DD_GIT_REPOSITORY_URL:'https://example.com/coverage-worker-reproduction.git',
      DD_GIT_COMMIT_SHA:'0123456789012345678901234567890123456789',
    };
    if (framework === 'cypress') {
      const instrument = spawnSync(process.execPath,[bin('nyc','bin/nyc.js'),'instrument','src','instrumented'],{cwd:directory,env,encoding:'utf8',timeout:30000});
      if(instrument.status!==0) throw new Error(instrument.stderr || 'instrumentation failed');
    }
    const [pattern, flags] = specifications[framework];
    const command = `node runner.cjs ${framework === 'cucumber' ? 'cucumber-js' : framework} ${flags}`;
    const argv = ['run','--platform','javascript','--framework',framework,
      '--min-parallelism',execution==='ci-node'?'1':'2','--max-parallelism',execution==='ci-node'?'1':'2',
      '--tests-location',pattern,'--worker-env','REPRO_WORKER={{workerIndex}}',
      '--coverage-output',path.join(directory,'ddtest-coverage'),'--command',command,
      ...(execution==='ci-node'?['--ci-node','0','--ci-node-workers','2']:[])];
    result.command=[ddtest,...argv];
    const fd=fs.openSync(path.join(directory,'ddtest.log'),'w');
    child=spawn(ddtest,argv,{cwd:directory,env,detached:true,stdio:['ignore',fd,fd]});
    fs.closeSync(fd); active.add(child);
    done=new Promise(resolve=>{
      child.on('error',error=>{result.error=error.message;active.delete(child);resolve();});
      child.on('exit',(code,signal)=>{result.exitCode=code;result.signal=signal;active.delete(child);resolve();});
    });
    const running={child,record:{worker:'ddtest'},finished:()=>result.exitCode!==undefined || result.signal || result.error};
    await until(()=>['alpha','beta'].every(w=>fs.existsSync(path.join(directory,`gate/${w}.ready`))),'both DDTest workers ready',[running]);
    fs.writeFileSync(path.join(directory,'gate/beta.release'),'release');
    await until(()=>fs.existsSync(path.join(directory,'gate/beta.exited')),'beta runner exit',[running]);
    const betaExit=JSON.parse(fs.readFileSync(path.join(directory,'gate/beta.exited')));
    if(betaExit.code!==0) throw new Error('Beta runner failed');
    fs.writeFileSync(path.join(directory,'gate/alpha.release'),'release');
    await until(()=>running.finished(),'DDTest exit',[],120000); await done;
    if(result.exitCode!==0 && !failWorker && !reportError) throw new Error('DDTest failed; see ddtest.log');
    if((failWorker || reportError) && result.exitCode===0) throw new Error('DDTest swallowed the expected failure');
    const output=path.join(directory,'ddtest-coverage');
    const sessions=fs.readdirSync(output).filter(f=>f.startsWith('ddtest-'));
    if(sessions.length!==1) throw new Error('Expected exactly one coverage session');
    const session=path.join(output,sessions[0]);
    if(reportError) {
      if(fs.existsSync(path.join(session,'report/coverage-final.json'))) throw new Error('Published incomplete coverage');
      const log=fs.readFileSync(path.join(directory,'ddtest.log'),'utf8');
      if(!log.includes('[run_coverage_merge_failed]') || !log.includes('Cannot merge coverage')) throw new Error('Unexpected failure instead of merge rejection');
      result.covered=[];result.status='merge-error-rejected';return result;
    }
    result.covered=coveredFiles(JSON.parse(fs.readFileSync(path.join(session,'report/coverage-final.json'))));
    if(result.covered.length!==2) throw new Error('Combined coverage lost a worker');
    result.workerReports=fs.readdirSync(session).filter(f=>/^node-\d+-worker-\d+$/.test(f));
    if(result.workerReports.length!==2) throw new Error('Expected two isolated workers');
    if(failWorker && !fs.readFileSync(path.join(directory,'ddtest.log'),'utf8').includes('Intentional alpha test failure')) throw new Error('Unrelated worker failure');
    result.status=failWorker?'failure-preserved':'fixed';
  } catch(error) { result.status='error';result.error=error.message; }
  finally { if(child)kill(child);if(done)await done;fs.writeFileSync(path.join(directory,'result.json'),JSON.stringify(result,null,2)); }
  console.log(`${framework.padEnd(10)} ddtest-${execution}: ${result.status} ${result.error || result.covered.join(', ')}`);
  return result;
}

(async () => {
  const versions = Object.fromEntries(Object.keys(require('./package.json').devDependencies).map(name =>
    [name, JSON.parse(fs.readFileSync(path.join(root, 'node_modules', name, 'package.json'))).version]));
  let server, url;
  if(ddtest) {
    server=require('node:http').createServer((req,res)=>{
      req.resume();
      const data=req.url.includes('search_commits')?[]:{attributes:{test_suites:{},itr_enabled:false,tests_skipping:false,require_git:false}};
      res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({data}));
    });
    await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
    url=`http://127.0.0.1:${server.address().port}`;
  }
  const summary = { node: process.version, platform: process.platform, versions, runRoot, cases: [] };
  console.log(`Evidence: ${runRoot}`);
  for (let iteration = 1; iteration <= repeat; iteration++) for (const framework of selected) {
    if (ddtest) {
      summary.cases.push(await runDDTestCase(framework,iteration,url));
      fs.writeFileSync(path.join(runRoot,'summary.json'),JSON.stringify(summary,null,2));
      continue;
    }
    const modes = ['shared', ...(framework === 'cypress' ? ['temp-only'] : []), 'isolated',
      ...(nycFrameworks.has(framework) ? ['shared-no-clean'] : [])];
    for (const mode of modes) {
      if (interrupted) return;
      summary.cases.push(await runCase(framework, mode, iteration));
      fs.writeFileSync(path.join(runRoot, 'summary.json'), JSON.stringify(summary, null, 2));
    }
  }
  if(server) await new Promise(resolve=>server.close(resolve));
  process.exitCode = summary.cases.some(c => c.status === 'error') ? 1 : 0;
})().catch(error => { console.error(error); for (const child of active) kill(child); process.exitCode = 1; });
