import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
// The observer records the CLI that actually started the run; the current
// folder's package may resolve another copy (nested packages, pnpm).
// Resolve its dependencies from its real location: pnpm links packages into
// node_modules, and a symlinked path can't see the CLI's own dependencies.
const cli = fs.realpathSync(process.env.DDTEST_WDIO_CLI || createRequire(path.join(process.cwd(), 'package.json')).resolve('@wdio/cli'));
const { resolve } = await import(pathToFileURL(createRequire(cli).resolve('import-meta-resolve')).href);
const localRunner = resolve('@wdio/local-runner', pathToFileURL(path.join(process.cwd(), 'package.json')).href);
const runner = resolve('@wdio/runner', localRunner);
const reporter = resolve('@wdio/reporter', resolve('@wdio/dot-reporter', runner));
const { default: Reporter } = await import(reporter);
export default class NativeReporter extends Reporter {
  results = new Map();
  files = new Map();
  identities = new Map();
  errors = [];
  retrying;
  constructor(options) {
    super(options);
    // TestStats drops the raw test.file. Capture it before the base reporter
    // converts events, including root-level tests with no file-bearing suite.
    for (const event of ['test:start', 'test:pending']) this.prependListener(event, test => {
      if (test.file) this.files.set(test.uid, test.file);
      this.identities.set(test.uid, this.retrying ?? test.uid);
      this.retrying = undefined;
    });
  }
  onRunnerStart(runner) { this.runner = runner; }
  record(test, status) {
    const parents = this.currentSuites.map(suite => suite.title).filter(title => title && title !== '(root)');
    const name = [...parents, test.title].join(' ');
    const localFile = file => file?.startsWith('file:') ? fileURLToPath(file) : file;
    const specs = this.runner.specs.map(localFile);
    // A single worker spec is authoritative. Jasmine can attribute wrapped
    // definitions to @wdio/utils; never use that wrapper as application source.
    const candidates = [this.files.get(test.uid), test.file, ...[...this.currentSuites].reverse().map(suite => suite.file)].filter(Boolean).map(localFile);
    const file = specs.length === 1 ? specs[0] : candidates.find(file => specs.some(spec => path.resolve(spec) === path.resolve(file)));
    const id = this.identities.get(test.uid) ?? test.uid;
    const attempts = [...(this.results.get(id)?.attempts || []), status];
    this.results.set(id, {id, file, name, status, attempts, failure: (test.errors || []).map(error => error.message || String(error)).join('\n')});
  }
  onTestRetry(test) { this.record(test, 'failed'); this.retrying = this.identities.get(test.uid) ?? test.uid; }
  onTestPass(test) { this.record(test, 'passed'); }
  onTestFail(test) { this.record(test, 'failed'); }
  onTestSkip(test) { this.record(test, 'skipped'); }
  onTestPending(test) { this.record(test, 'skipped'); }
  onHookEnd(hook) { if (hook.error) this.errors.push(hook.error.message || String(hook.error)); }
  onRunnerEnd() {
    const directory = process.env.DDTEST_WDIO_RESULTS;
    fs.mkdirSync(directory, {recursive: true});
    const metadata = JSON.parse(fs.readFileSync(process.env.DDTEST_WDIO_METADATA, 'utf8'));
    const selection = metadata.selections[Number(this.runner.cid.split('-')[0])];
    if (!selection) throw new Error('WDIO worker capability identity is unavailable');
    fs.writeFileSync(path.join(directory, `${this.runner.cid}-${process.pid}.json`), JSON.stringify({
      complete: true, cid: this.runner.cid, retry: this.runner.retry || 0,
      // Attempt numbering differs between WebdriverIO releases; ddtest uses
      // the worker's own setting to number the first attempt.
      specFileRetries: Number.isInteger(this.runner.config?.specFileRetries) ? this.runner.config.specFileRetries : undefined,
      specs: this.runner.specs.map(file => file.startsWith('file:') ? fileURLToPath(file) : file),
      browser: selection.browser, capability: selection.capability,
      tests: [...this.results.values()], errors: this.errors
    }));
  }
}
