const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const localRequire = createRequire(path.join(process.cwd(), 'package.json'));
const { Cli, Formatter } = localRequire('@cucumber/cucumber');
// npm forwards arguments to the main script, never its lifecycle hooks. Add
// formatters only when this process is the package's actual CLI entry point.
// Wrapper, npm, and parallel-worker processes retain their original argv.
if (process.env.DDTEST_CUCUMBER_FORMATTERS && process.argv[1]) {
  const manifest = localRequire.resolve('@cucumber/cucumber/package.json');
  const definition = localRequire(manifest);
  const binary = typeof definition.bin === 'string' ? definition.bin : definition.bin['cucumber-js'];
  let entry;
  try { entry = fs.realpathSync(process.argv[1]); } catch { /* node -e may expose a non-file argument here */ }
  if (entry === fs.realpathSync(path.resolve(path.dirname(manifest), binary))) {
    const args = JSON.parse(process.env.DDTEST_CUCUMBER_FORMATTERS);
    if (!Array.isArray(args) || args.some(arg => typeof arg !== 'string')) {
      throw new Error('Invalid native Cucumber formatter options');
    }
    process.argv.push(...args);
  }
}

let legacySupport;
// Cucumber 7 exposes resolved support paths through its CLI. Later releases
// expose the same coordinates on the formatter's support-code library.
if (typeof Cli?.prototype.getConfiguration === 'function') {
  const original = Cli.prototype.getConfiguration;
  Cli.prototype.getConfiguration = async function (...args) {
    const config = await original.apply(this, args);
    legacySupport = {requirePaths: config.supportCodePaths, importPaths: []};
    return config;
  };
}
module.exports = class SupportMetadata extends Formatter {
  constructor(options) {
    super(options);
    const support = options.supportCodeLibrary.originalCoordinates || legacySupport;
    if (!support || !Array.isArray(support.requirePaths) || !Array.isArray(support.importPaths)) {
      throw new Error('Cucumber effective support paths are unavailable');
    }
    const args = process.argv.slice(2);
    const configFiles = [];
    for (let i = 0; i < args.length; i++) {
      if (args[i] === '--config' || args[i] === '-c') configFiles.push(args[++i]);
      else if (args[i].startsWith('--config=')) configFiles.push(args[i].slice(9));
    }
    if (!configFiles.length) {
      const file = ['cucumber.js','cucumber.cjs','cucumber.mjs','cucumber.json','cucumber.yaml','cucumber.yml']
        .find(file => fs.existsSync(path.resolve(file)));
      if (file) configFiles.push(file);
    }
    fs.writeFileSync(process.env.DDTEST_CUCUMBER_SUPPORT, JSON.stringify({
      requirePaths: support.requirePaths, importPaths: support.importPaths,
      requireModules: support.requireModules || [], loaders: support.loaders || [],
      configFiles: configFiles.map(file => path.resolve(file))
    }));
  }
};
