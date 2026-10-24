import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL, fileURLToPath } from 'node:url';

// @wdio/static-server-service listens on 4567 unless a port is configured,
// including when it is listed by name without options.
export function staticServerPorts(services = []) {
  return services.map(service => Array.isArray(service) ? service : [service])
    .filter(([name]) => name === 'static-server').map(([, options]) => Number(options?.port ?? 4567));
}

// Windows entry paths use backslashes; match the WDIO CLI and worker in either form.
export function wdioEntryPoint(entry) {
  const normalized = entry.replaceAll('\\', '/');
  if (normalized.includes('/@wdio/cli/')) return 'cli';
  return normalized.includes('/@wdio/local-runner/') ? 'worker' : '';
}

// Entry-point resolution selects the same dependency graph as the actual CLI,
// including nested/pnpm installations. Ordinary npm/application processes do nothing.
export async function installObserver(cli) {
  const cliRequire = createRequire(cli);
  const { resolve } = await import(pathToFileURL(cliRequire.resolve('import-meta-resolve')).href);
  const { ConfigParser } = await import(resolve('@wdio/config/node', pathToFileURL(cli).href));
  process.env.DDTEST_WDIO_CLI = cli;
  const initialize = ConfigParser.prototype.initialize;
  ConfigParser.prototype.initialize = async function (...args) {
    await initialize.apply(this, args);
    // WDIO first checks config accessibility, before applying CLI options.
    if (!args[0] || Object.keys(args[0]).length === 0) return;
    const config = this.getConfig();
    const runner = Array.isArray(config.runner) ? config.runner[0] : config.runner;
    const capabilities = this.getCapabilities();
    // Keep the original configuration index even when a probe selects one cap.
    // This distinguishes equal browser names without retaining secret options.
    const ids = new Map(Array.isArray(capabilities) ? capabilities.map((cap, i) => [cap, String(i)]) : []);
    const localFile = file => file.startsWith('file:') ? fileURLToPath(file) : file;
    const selections = () => Array.isArray(capabilities) ? capabilities.map(cap => ({
      capability: ids.get(cap), browser: cap.browserName,
      files: this.getSpecs(cap.specs, cap.exclude).flat(Infinity).map(localFile)
    })) : [];
    // Multiremote (object-form capabilities) runs every instance in one session
    // over the global selection; it has no per-capability specs.
    const multiremote = !Array.isArray(capabilities) && capabilities != null && typeof capabilities === 'object';
    // A grouped spec ([['setup.js', 'case.js']]) runs its files in one worker.
    const grouped = () => (multiremote ? [this.getSpecs()] : Array.isArray(capabilities) ? capabilities.map(cap => this.getSpecs(cap.specs, cap.exclude)) : [])
      .some(specs => specs.some(Array.isArray));
    const cloudGrid = [];
    // Connection options may also be set per capability (or per multiremote
    // instance), overriding the top-level ones; check every level.
    const connections = [config, ...(Array.isArray(capabilities) ? capabilities : Object.values(capabilities || {}))];
    const localHosts = ['localhost', '127.0.0.1', '::1', '[::1]', '0.0.0.0'];
    if (connections.some(options => options?.hostname && !localHosts.includes(options.hostname))) cloudGrid.push('remote WebDriver host');
    if (connections.some(options => options?.user || options?.key)) cloudGrid.push('WebDriver credentials');
    const cloudNames = /sauce|browserstack|lambdatest|testingbot/i;
    if ((config.services || []).some(service => cloudNames.test(String(Array.isArray(service) ? service[0] : service)))) cloudGrid.push('cloud provider service');
    const caps = Array.isArray(capabilities) ? capabilities : Object.values(capabilities || {}).map(value => value.capabilities || value);
    if (caps.some(cap => Object.keys(cap).some(key => /^(?:sauce|bstack|browserstack|lt|tb):/i.test(key)))) cloudGrid.push('cloud provider capability');
    const describe = () => ({
      cloud_grid: cloudGrid,
      node: process.version,
      version: JSON.parse(fs.readFileSync(path.join(path.dirname(cli), '../package.json'), 'utf8')).version,
      runner, framework: config.framework,
      files: multiremote ? [...new Set(this.getSpecs().flat(Infinity).map(localFile))] : [...new Set(selections().flatMap(selection => selection.files))],
      grouped_specs: grouped(),
      multiremote,
      static_server_ports: staticServerPorts(config.services),
      selections: selections(),
      capabilities: Array.isArray(capabilities) ? capabilities.map(cap => Object.fromEntries(
        ['browserName', 'browserVersion', 'platformName'].filter(key => typeof cap[key] === 'string').map(key => [key, cap[key]])
      )) : []
    });
    if (process.env.DDTEST_WDIO_INSPECT) {
      fs.writeFileSync(process.env.DDTEST_WDIO_INSPECT, JSON.stringify(describe()));
      process.exit(0);
    }
    // Config inspection exits above, before services or sessions can start.
    if (cloudGrid.length) throw new Error('Cloud grids are not supported locally: ' + cloudGrid.join(', '));
    if (!config.cacheDir && process.env.DDTEST_WDIO_CACHE) config.cacheDir = process.env.DDTEST_WDIO_CACHE;
    const files = process.env.DDTEST_WDIO_PROBE ? [process.env.DDTEST_WDIO_PROBE]
      : process.env.DDTEST_WDIO_FILES ? JSON.parse(process.env.DDTEST_WDIO_FILES) : null;
    if (files) {
      // A planned batch keeps each capability's own selection: each one runs only
      // the assigned files it would select itself. Probes replace every selection.
      const real = file => { try { return fs.realpathSync(localFile(file)); } catch { return localFile(file); } };
      const own = !process.env.DDTEST_WDIO_PROBE && Array.isArray(capabilities)
        ? capabilities.map(cap => new Set(this.getSpecs(cap.specs, cap.exclude).flat(Infinity).map(real)))
        : null;
      config.specs = config.spec = files;
      config.exclude = []; config.suite = [];
      if (own) {
        // The spec list overrides capability specs, so drop the files a
        // capability would not select through its exclude list instead.
        const kept = capabilities.filter((cap, i) => {
          cap.specs = files; cap.exclude = files.filter(file => !own[i].has(real(file)));
          return cap.exclude.length < files.length;
        });
        if (kept.length === 0) throw new Error('No WDIO capability selects the assigned spec files');
        capabilities.splice(0, capabilities.length, ...kept);
      } else if (Array.isArray(capabilities)) for (const cap of capabilities) {
        cap.specs = files; cap.exclude = [];
      }
      // The batch is already this runner's share; native sharding would split it again.
      if (!process.env.DDTEST_WDIO_PROBE) config.shard = {current: 1, total: 1};
    }
    if (process.env.DDTEST_WDIO_PROBE) {
      config.specFileRetries = 0;
      config.repeat = 0; config.shard = {current: 1, total: 1};
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
}
let entry = process.argv[1] ? path.resolve(process.argv[1]) : '';
try { entry = fs.realpathSync(entry); } catch {}
const entryPoint = wdioEntryPoint(entry);
if (entryPoint) {
  const require = createRequire(entry);
  const cli = entryPoint === 'cli' ? require.resolve('@wdio/cli') : process.env.DDTEST_WDIO_CLI || require.resolve('@wdio/cli');
  await installObserver(cli);
}
