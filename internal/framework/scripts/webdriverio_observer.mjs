import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL, fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.cwd(), 'package.json'));
const { resolve } = await import(pathToFileURL(require.resolve('import-meta-resolve')).href);
const { ConfigParser } = await import(resolve('@wdio/config/node', pathToFileURL(path.join(process.cwd(), 'package.json')).href));
const initialize = ConfigParser.prototype.initialize;
ConfigParser.prototype.initialize = async function (...args) {
  await initialize.apply(this, args);
  // WDIO probes config accessibility before applying CLI options. Observe the
  // final initialize call, not that preliminary unfiltered configuration.
  if (!args[0] || Object.keys(args[0]).length === 0) return;
  const config = this.getConfig();
  const runner = Array.isArray(config.runner) ? config.runner[0] : config.runner;
  const capabilities = this.getCapabilities();
  const localFile = file => file.startsWith('file:') ? fileURLToPath(file) : file;
  const describe = () => ({
    version: JSON.parse(fs.readFileSync(new URL('../package.json', resolve('@wdio/cli', pathToFileURL(path.join(process.cwd(), 'package.json')).href)), 'utf8')).version,
    runner, framework: config.framework,
    files: this.getSpecs().flat(Infinity).map(localFile),
    static_server_ports: config.services.filter(service => Array.isArray(service) && service[0] === 'static-server' && service[1]?.port != null)
      .map(service => Number(service[1].port)),
    selections: Array.isArray(capabilities) ? capabilities.map(cap => ({browser: cap.browserName, files: this.getSpecs(cap.specs, cap.exclude).flat(Infinity).map(localFile)})) : [],
    capabilities: Array.isArray(capabilities) ? capabilities.map(cap => Object.fromEntries(
      ['browserName', 'browserVersion', 'platformName'].filter(key => typeof cap[key] === 'string').map(key => [key, cap[key]])
    )) : []
  });
  if (process.env.DDTEST_WDIO_INSPECT) {
    fs.writeFileSync(process.env.DDTEST_WDIO_INSPECT, JSON.stringify(describe()));
    process.exit(0);
  }
  if (process.env.DDTEST_WDIO_FILES) {
    config.specs = config.spec = JSON.parse(process.env.DDTEST_WDIO_FILES);
    config.suite = [];
  }
  if (process.env.DDTEST_WDIO_PROBE) {
    config.specs = config.spec = [process.env.DDTEST_WDIO_PROBE];
    config.exclude = []; config.suite = []; config.specFileRetries = 0;
    if (config.mochaOpts) config.mochaOpts = {...config.mochaOpts, grep: '^ddtest validation probe$', invert: false, retries: 0};
    if (config.jasmineOpts) config.jasmineOpts = {...config.jasmineOpts, grep: '^ddtest validation probe$', invertGrep: false};
  }
  if (process.env.DDTEST_WDIO_CAPABILITY) {
    const selected = capabilities[Number(process.env.DDTEST_WDIO_CAPABILITY)];
    if (!selected) throw new Error('The selected WDIO capability is unavailable');
    capabilities.splice(0, capabilities.length, selected);
  }
  if (process.env.DDTEST_WDIO_METADATA && !fs.existsSync(process.env.DDTEST_WDIO_METADATA)) {
    fs.writeFileSync(process.env.DDTEST_WDIO_METADATA, JSON.stringify(describe()));
  }
  const directory = process.env.DDTEST_WDIO_RESULTS;
  if (directory) {
    const reporter = fileURLToPath(new URL('./webdriverio_reporter.mjs', import.meta.url));
    if (!config.reporters.some(item => (Array.isArray(item) ? item[0] : item) === reporter)) {
      config.reporters = [...config.reporters, [reporter, {stdout: true}]];
    }
    config.outputDir = path.join(directory, 'logs');
  }
};
