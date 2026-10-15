import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
const require = createRequire(path.join(process.cwd(), 'package.json'));
const { resolve } = await import(pathToFileURL(require.resolve('import-meta-resolve')).href);
const { default: Reporter } = await import(resolve('@wdio/reporter', pathToFileURL(path.join(process.cwd(), 'package.json')).href));
export default class NativeReporter extends Reporter {
  results = new Map();
  errors = [];
  onRunnerStart(runner) { this.runner = runner; }
  record(test, status) {
    const parents = this.currentSuites.map(suite => suite.title).filter(title => title && title !== '(root)');
    const name = [...parents, test.title].join(' ');
    let file = this.runner.specs.length === 1 ? this.runner.specs[0] : [...this.currentSuites].reverse().find(suite => suite.file)?.file;
    if (file?.startsWith('file:')) file = fileURLToPath(file);
    this.results.set(file + '\0' + name, {file, name, status, failure: (test.errors || []).map(error => error.message || String(error)).join('\n')});
  }
  onTestPass(test) { this.record(test, 'passed'); }
  onTestFail(test) { this.record(test, 'failed'); }
  onTestSkip(test) { this.record(test, 'skipped'); }
  onHookEnd(hook) { if (hook.error) this.errors.push(hook.error.message || String(hook.error)); }
  onRunnerEnd() {
    const directory = process.env.DDTEST_WDIO_RESULTS;
    fs.mkdirSync(directory, {recursive: true});
    fs.writeFileSync(path.join(directory, `${this.runner.cid}-${process.pid}.json`), JSON.stringify({
      complete: true, specs: this.runner.specs.map(file => file.startsWith('file:') ? fileURLToPath(file) : file),
      browser: this.runner.capabilities.browserName,
      tests: [...this.results.values()], errors: this.errors
    }));
  }
}
