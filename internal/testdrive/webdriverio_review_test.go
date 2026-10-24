package testdrive

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestWebdriverIONativeRetriesVerifyAttemptsWithoutChangingFinalOutcome(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "native")
	require.NoError(t, os.Mkdir(directory, 0700))
	requireWriteFile(t, filepath.Join(root, "metadata.json"), `{"selections":[{"capability":"0","browser":"msedge","files":["spec.js"]}]}`)
	requireWriteFile(t, filepath.Join(directory, "worker.json"), `{"complete":true,"capability":"0","browser":"msedge","specs":["spec.js"],"tests":[{"id":"a","file":"spec.js","name":"same","status":"passed","attempts":["failed","passed"]},{"id":"b","file":"spec.js","name":"same","status":"passed","attempts":["passed"]}]}`)
	run := validationRun{root: root, framework: "webdriverio"}
	readWebdriverIOResults(directory, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 2)
	require.Len(t, run.webdriverIOAttempts, 3)
	run.Facts = intake.Facts{TestEventCount: 3, Tests: []intake.Test{{Name: "same", SourceFile: "spec.js", Attempts: []intake.TestRun{{Status: "fail"}, {Status: "pass", Retry: true, RetryReason: "external"}, {Status: "pass"}}}}}
	require.Empty(t, verifyJestTelemetry(run).Status)
	run.Facts.Tests[0].Attempts[1].RetryReason = "early_flake_detection"
	require.Equal(t, "unexpected_retry", verifyJestTelemetry(run).Code)
}

func TestWebdriverIOObserverIgnoresOtherNodeProcesses(t *testing.T) {
	root := t.TempDir()
	observer, err := framework.PrepareWebdriverIOObserver(root)
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), "node", "--import", observer, "-e", "console.log('unrelated process')")
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	require.Equal(t, "unrelated process", strings.TrimSpace(string(data)))
}

func TestWebdriverIOCloudInspectionStopsBeforeLauncherHooks(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	for _, config := range []string{`hostname:'ondemand.saucelabs.com',user:'fake-user',key:'fake-key'`, `services:['browserstack']`, `capabilities:[{browserName:'chrome','sauce:options':{username:'fake-user'}}]`,
		// Capability-level connection options override the top-level ones.
		`capabilities:[{browserName:'chrome',hostname:'hub.example.com',port:4444}]`, `capabilities:[{browserName:'chrome',user:'fake-user',key:'fake-key'}]`} {
		root := t.TempDir()
		require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
		requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
		requireWriteFile(t, filepath.Join(root, "wdio.conf.cjs"), `exports.config={runner:'local',framework:'mocha',specs:['./one.js'],capabilities:[{browserName:'chrome'}],onPrepare(){require('node:fs').writeFileSync('must-not-run','bad')},`+config+`}`)
		requireWriteFile(t, filepath.Join(root, "one.js"), "")
		observer, err := framework.PrepareWebdriverIOObserver(root)
		require.NoError(t, err)
		cmd := exec.CommandContext(t.Context(), "node", "--import", observer, filepath.Join(modules, "@wdio/cli/bin/wdio.js"), "run", "wdio.conf.cjs")
		cmd.Dir = root
		output := filepath.Join(root, "config.json")
		cmd.Env = append(os.Environ(), "DDTEST_WDIO_INSPECT="+output)
		data, err := cmd.CombinedOutput()
		require.NoError(t, err, string(data))
		var effective framework.WebdriverIOConfig
		data, err = os.ReadFile(output)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &effective))
		require.NotEmpty(t, effective.CloudGrid)
		require.NotContains(t, string(data), "fake-key")
		verdict := checkWebdriverIOSupport(jestPreflight{WebdriverIO: &effective}, platform.JSSelection{})
		require.Equal(t, "cloud_grid_unsupported", verdict.Code)
		cmd = exec.CommandContext(t.Context(), "node", "--import", observer, filepath.Join(modules, "@wdio/cli/bin/wdio.js"), "run", "wdio.conf.cjs")
		cmd.Dir = root
		data, err = cmd.CombinedOutput()
		require.Error(t, err, string(data))
		require.Contains(t, string(data), "Cloud grids are not supported locally")
		require.NoFileExists(t, filepath.Join(root, "must-not-run"))
	}
}

func TestWebdriverIORawReporterEventsKeepGroupedRootSourcesAndNativeRetries(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES")
	}
	root := t.TempDir()
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
	_, err = framework.PrepareWebdriverIOObserver(root)
	require.NoError(t, err)
	script := `const {default:Reporter}=await import('./webdriverio_reporter.mjs');
 const r=new Reporter({stdout:true,writeStream:process.stdout});
 r.emit('runner:start',{cid:'0-0',specs:['one.js','two.js'],capabilities:{browserName:'MicrosoftEdge'}});
 r.emit('test:start',{uid:'a',title:'same',file:'one.js'});
 r.emit('test:retry',{uid:'a',title:'same',error:{message:'first attempt'}});
 r.emit('test:start',{uid:'b',title:'same',file:'one.js'});
 r.emit('test:pass',{uid:'b',title:'same'});
 r.emit('test:start',{uid:'c',title:'same',file:'two.js'});
 r.emit('test:pass',{uid:'c',title:'same'});
 const tests=[...r.results.values()];
 if(tests.length!==2||tests[0].file!=='one.js'||tests[1].file!=='two.js'||tests[0].attempts.join(',')!=='failed,passed')throw Error(JSON.stringify(tests));`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script)
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
}

func TestWebdriverIOProbeOverridesSelectionAndPreservesReportersAndCache(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
	requireWriteFile(t, filepath.Join(root, "one.js"), "")
	requireWriteFile(t, filepath.Join(root, "wdio.conf.cjs"), `exports.config={runner:'local',framework:'mocha',specs:[],spec:['./one.js'],repeat:2,shard:{current:2,total:2},reporters:['spec'],capabilities:[{browserName:'msedge',specs:['./one.js'],exclude:['**/one.js']},{browserName:'chrome',specs:['./one.js'],exclude:['**/one.js']}],mochaOpts:{ui:'tdd',grep:'customer',invert:true,retries:2}}`)
	_, err = framework.PrepareWebdriverIOObserver(root)
	require.NoError(t, err)
	script := `import assert from 'node:assert/strict';import {createRequire} from 'node:module';import {pathToFileURL,fileURLToPath} from 'node:url';
 const r=createRequire(process.cwd()+'/package.json'),cli=r.resolve('@wdio/cli');
 const {resolve}=await import(pathToFileURL(createRequire(cli).resolve('import-meta-resolve')).href);
 const {installObserver}=await import('./webdriverio_observer.mjs');await installObserver(cli);
 const {ConfigParser}=await import(resolve('@wdio/config/node',pathToFileURL(cli).href));
 const parser=new ConfigParser(process.cwd()+'/wdio.conf.cjs');await parser.initialize({logLevel:'silent'});
 const config=parser.getConfig(),caps=parser.getCapabilities();
 assert.equal(config.repeat,0);assert.deepEqual(config.shard,{current:1,total:1});
 assert.equal(config.cacheDir,process.env.DDTEST_WDIO_CACHE);assert.equal(config.mochaOpts.ui,'tdd');
 assert.equal(config.mochaOpts.retries,0);assert.equal(config.mochaOpts.invert,false);
 assert.equal(config.reporters[0],'spec');assert.equal(config.reporters.length,2);
 assert.equal(caps.length,1);assert.equal(caps[0].browserName,'chrome');
 assert.deepEqual(parser.getSpecs(caps[0].specs,caps[0].exclude).map(file=>file.startsWith('file:')?fileURLToPath(file):file),[process.env.DDTEST_WDIO_PROBE]);`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script)
	cmd.Dir = root
	metadata := filepath.Join(root, "metadata.json")
	cmd.Env = append(os.Environ(), "DDTEST_WDIO_PROBE="+filepath.Join(root, "one.js"), "DDTEST_WDIO_CAPABILITY=1", "DDTEST_WDIO_CACHE="+filepath.Join(root, "cache"), "DDTEST_WDIO_RESULTS="+filepath.Join(root, "results"), "DDTEST_WDIO_METADATA="+metadata)
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	data, err = os.ReadFile(metadata)
	require.NoError(t, err)
	var report struct {
		Selections []struct{ Capability, Browser string }
	}
	require.NoError(t, json.Unmarshal(data, &report))
	require.Len(t, report.Selections, 1)
	require.Equal(t, "1", report.Selections[0].Capability)
	require.Equal(t, "chrome", report.Selections[0].Browser)
}
