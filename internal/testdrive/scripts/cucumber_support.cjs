const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');

// NODE_OPTIONS preloads this file into every Node process of the run: npm,
// coverage wrappers, and servers or helpers that hooks start from any folder.
// Only the package's actual Cucumber CLI entry loads Cucumber; every other
// process runs unchanged, even where @cucumber/cucumber cannot be resolved.
function cucumberEntry () {
  if (!process.argv[1]) return undefined;
  try {
    const localRequire = createRequire(path.join(process.cwd(), 'package.json'));
    const manifest = localRequire.resolve('@cucumber/cucumber/package.json');
    const definition = JSON.parse(fs.readFileSync(manifest, 'utf8'));
    const binary = typeof definition.bin === 'string' ? definition.bin : definition.bin['cucumber-js'];
    if (fs.realpathSync(process.argv[1]) === fs.realpathSync(path.resolve(path.dirname(manifest), binary))) {
      return localRequire;
    }
  } catch { /* Cucumber is not resolvable here, or node -e exposes a non-file argument */ }
  return undefined;
}

const localRequire = cucumberEntry();
if (localRequire) {
  const { Cli, Formatter } = localRequire('@cucumber/cucumber');
  // npm forwards arguments to the main script, never its lifecycle hooks.
  // Wrapper, npm and parallel-worker processes retain their original argv.
  if (process.env.DDTEST_CUCUMBER_FORMATTERS) {
    const args = JSON.parse(process.env.DDTEST_CUCUMBER_FORMATTERS);
    if (!Array.isArray(args) || args.some(arg => typeof arg !== 'string')) {
      throw new Error('Invalid native Cucumber formatter options');
    }
    process.argv.push(...args);
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

  // Probes replace the customer's profiles with an empty configuration. Keep
  // the profile options that World and hooks need to start and to finish;
  // selection and retries stay out. Command-line values are replayed by ddtest.
  async function runOptions (file, profiles) {
    let api;
    try { api = localRequire('@cucumber/cucumber/api'); } catch { return {}; }
    if (typeof api.loadConfiguration !== 'function') return {};
    const { useConfiguration } = await api.loadConfiguration({ file: file || false, profiles });
    const options = {};
    if (useConfiguration.forceExit) options.forceExit = true;
    if (useConfiguration.worldParameters && Object.keys(useConfiguration.worldParameters).length) {
      options.worldParameters = useConfiguration.worldParameters;
    }
    return options;
  }

  module.exports = class SupportMetadata extends Formatter {
    constructor (options) {
      super(options);
      const support = options.supportCodeLibrary.originalCoordinates || legacySupport;
      if (!support || !Array.isArray(support.requirePaths) || !Array.isArray(support.importPaths)) {
        throw new Error('Cucumber effective support paths are unavailable');
      }
      const args = process.argv.slice(2);
      const configFiles = [];
      const profiles = [];
      for (let i = 0; i < args.length; i++) {
        if (args[i] === '--config' || args[i] === '-c') configFiles.push(args[++i]);
        else if (args[i].startsWith('--config=')) configFiles.push(args[i].slice(9));
        else if (args[i] === '--profile' || args[i] === '-p') profiles.push(args[++i]);
        else if (args[i].startsWith('--profile=')) profiles.push(args[i].slice(10));
      }
      if (!configFiles.length) {
        const file = ['cucumber.js','cucumber.cjs','cucumber.mjs','cucumber.json','cucumber.yaml','cucumber.yml']
          .find(file => fs.existsSync(path.resolve(file)));
        if (file) configFiles.push(file);
      }
      this.metadata = {
        requirePaths: support.requirePaths, importPaths: support.importPaths,
        requireModules: support.requireModules || [], loaders: support.loaders || [],
        configFiles: configFiles.map(file => path.resolve(file))
      };
      fs.writeFileSync(process.env.DDTEST_CUCUMBER_SUPPORT, JSON.stringify(this.metadata));
      this.runOptions = runOptions(configFiles[0], profiles).catch(error => ({ error: String(error?.message || error) }));
    }

    // Cucumber awaits formatters before it exits, including with forceExit.
    async finished () {
      const options = await this.runOptions;
      if (options.error) this.metadata.runOptionsError = options.error;
      else this.metadata.runOptions = options;
      fs.writeFileSync(process.env.DDTEST_CUCUMBER_SUPPORT, JSON.stringify(this.metadata));
      return super.finished();
    }
  };
}
