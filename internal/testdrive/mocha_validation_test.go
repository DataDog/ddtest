// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestMochaNativeResultsPreserveDuplicatesAndErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0600))
	}
	write("1.json", `{"complete":true,"tests":[{"file":"/repo/test.js","name":"same","status":"passed"},{"file":"/repo/test.js","name":"same","status":"passed"}],"errors":[]}`)
	write("2.json", `{"complete":true,"tests":[{"file":"/repo/esm.mjs","name":"failure","status":"failed","failure":"expected 1 to equal 2\n at file.js"}],"errors":["before hook: setup failed"]}`)
	var run validationRun
	readMochaResults(dir, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 3)
	require.Equal(t, "expected 1 to equal 2", run.Tests[2].Failure)
	require.Equal(t, []string{"before hook: setup failed"}, run.SuiteErrors)
	for _, content := range []string{`{"complete":false,"tests":[]}`, `{"complete":true}`, `{"complete":true,"tests":null}`, `invalid`} {
		write("3.json", content)
		run = validationRun{}
		readMochaResults(dir, &run)
		require.NotEmpty(t, run.ResultError)
	}
	run = validationRun{}
	readMochaResults(t.TempDir(), &run)
	require.NotEmpty(t, run.ResultError)
}

func TestMochaSupportUsesTracerBoundaries(t *testing.T) {
	for _, tc := range []struct{ mocha, tracer, node, want string }{
		{"5.1.0", "5.130.0", ">=18", "incompatible"},
		{"5.2.0", "5.130.0", ">=18", "compatible"},
		{"7.0.0", "6.20.0", ">=22", "incompatible"},
		{"8.0.0", "6.20.0", ">=22", "compatible"},
		{"12.0.0", "6.20.0", ">=22", "compatible"},
		{"99.0.0", "6.20.0", ">=26", "incompatible"},
	} {
		require.Equal(t, tc.want, checkMochaSupport(jestPreflight{Version: tc.mocha, Node: "24.14.1"}, platform.JSSelection{Version: tc.tracer, Node: tc.node}).Status)
	}
}

func TestMochaObserverUsesNativeEventsAndKeepsConfiguration(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	dir := filepath.Join(root, "node_modules/mocha/lib/cli")
	require.NoError(t, os.MkdirAll(dir, 0700))
	requireWriteFile(t, filepath.Join(root, "node_modules/mocha/package.json"), `{"name":"mocha","version":"11.8.0"}`)
	requireWriteFile(t, filepath.Join(root, "node_modules/mocha/lib/runner.js"), `const {EventEmitter}=require('node:events'); module.exports=class Runner extends EventEmitter { run(fn){fn(this);return this} }`)
	requireWriteFile(t, filepath.Join(dir, "options.js"), `exports.loadOptions=()=>({_:['original.js'],spec:['another.js'],ui:'bdd',require:['setup.js'],reporter:'custom',timeout:1234,retries:3,parallel:true,grep:'original',invert:true})`)
	requireWriteFile(t, filepath.Join(root, "node_modules/mocha/lib/mocha.js"), `module.exports=class Mocha { constructor(){this.options={ui:"bdd",timeout:1234,reporter:"custom"};this.files=["original.js"];this.suite={retries:n=>this.retryCount=n}} loadFiles(){return this.files} run(fn){fn(this);return this} }; module.exports.Runner=require('./runner')`)
	observer := filepath.Join(root, "observer.cjs")
	requireWriteFile(t, observer, mochaObserver)
	requireWriteFile(t, filepath.Join(root, "run.cjs"), `
const assert=require('node:assert/strict'); const Runner=require('mocha/lib/runner');
const Mocha=require('mocha/lib/mocha'); const mocha=new Mocha();
assert.deepEqual(mocha.loadFiles(),[process.env.DDTEST_MOCHA_PROBE]);
assert.equal(mocha.retryCount,0); assert.equal(mocha.options.timeout,1234);
assert.equal(mocha.options.reporter,'custom');
mocha.files=[]; assert.deepEqual(mocha.loadFiles(),[]);
let callback=false; assert.equal(mocha.run(m=>{callback=true;assert.equal(m,mocha)}),mocha); assert.ok(callback);
const options=require('mocha/lib/cli/options').loadOptions();
assert.deepEqual(options._,[process.env.DDTEST_MOCHA_PROBE]); assert.equal(options.spec,undefined);
assert.deepEqual(options.require,['setup.js']); assert.equal(options.reporter,'custom');
assert.equal(options.timeout,1234); assert.equal(options.parallel,true); assert.equal(options.invert,false);
const test={type:'test',file:'test.js',fullTitle:()=> 'same'};
const runner=new Runner();runner.run(r=>{r.emit('pass',test);r.emit('pass',test);r.emit('pending',{...test,fullTitle:()=> 'pending'});r.emit('fail',{...test,type:'hook',fullTitle:()=> 'before hook'},new Error('setup'));r.emit('end')});
runner.run(r=>{r.emit('pass',{...test,fullTitle:()=> 'reused runner'});r.emit('end')});
`)
	command := exec.CommandContext(t.Context(), node, "--require", observer, "run.cjs")
	command.Dir = root
	command.Env = append(os.Environ(), "NODE_OPTIONS=", "DDTEST_MOCHA_RESULTS="+root, "DDTEST_MOCHA_PROBE=/repo/probe.js")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	var run validationRun
	readMochaResults(root, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 4)
	require.Equal(t, "pending", run.Tests[2].Status)
	require.Equal(t, "reused runner", run.Tests[3].Name)
	require.Equal(t, []string{"before hook: setup"}, run.SuiteErrors)
}

// Mocha 12.0.3 ships lib/runner.js as an ES module, so requiring it returns a
// namespace without a prototype; the observer must use Mocha.Runner instead.
func TestMochaObserverFindsAnESMRunner(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	lib := filepath.Join(root, "node_modules/mocha/lib")
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "cli"), 0700))
	requireWriteFile(t, filepath.Join(root, "node_modules/mocha/package.json"), `{"name":"mocha","version":"12.0.3","type":"module"}`)
	requireWriteFile(t, filepath.Join(lib, "runner.js"), `import {EventEmitter} from 'node:events'; export class Runner extends EventEmitter { run(fn){fn(this);return this} }`)
	requireWriteFile(t, filepath.Join(lib, "mocha.cjs"), `class Mocha { run(fn){fn(this);return this} }; Mocha.Runner=require('./runner.js').Runner; module.exports=Mocha`)
	observer := filepath.Join(root, "observer.cjs")
	requireWriteFile(t, observer, mochaObserver)
	requireWriteFile(t, filepath.Join(root, "run.cjs"), `const Runner=require('mocha/lib/mocha.cjs').Runner;
const runner=new Runner();runner.run(r=>{r.emit('pass',{type:'test',file:'test.js',fullTitle:()=> 'esm runner'});r.emit('end')});`)
	command := exec.CommandContext(t.Context(), node, "--require", observer, "run.cjs")
	command.Dir = root
	command.Env = append(os.Environ(), "NODE_OPTIONS=", "DDTEST_MOCHA_RESULTS="+root)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	var run validationRun
	readMochaResults(root, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 1)
	require.Equal(t, "esm runner", run.Tests[0].Name)
}

type mochaScratchExecutor struct {
	temporary  string
	commandErr error
}

func (e *mochaScratchExecutor) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	e.temporary = env["TMPDIR"]
	if env["XDG_CACHE_HOME"] != filepath.Join(e.temporary, "cache") {
		return nil, errors.New("cache escapes owned temporary directory")
	}
	if env["TMP"] != e.temporary || env["TEMP"] != e.temporary {
		return nil, errors.New("temporary directories disagree")
	}
	if err := os.WriteFile(filepath.Join(e.temporary, "tracer-cache.json"), []byte("{}"), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(env["DDTEST_MOCHA_RESULTS"], "native.json"), []byte(`{"complete":true,"tests":[]}`), 0600); err != nil {
		return nil, err
	}
	return []byte("runner output"), e.commandErr
}
func TestMochaChildScratchIsOwnedBySession(t *testing.T) {
	for _, fail := range []bool{false, true} {
		drive := preparedTestdrive(t)
		drive.framework = framework.NewMocha(platform.NewJavaScript())
		executor := &mochaScratchExecutor{}
		if fail {
			executor.commandErr = errors.New("runner failed")
		}
		drive.executor = executor
		drive.startIntake = func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }
		session, err := workspace.NewSession()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close()) })
		_, err = drive.runMocha(t.Context(), io.Discard, session, "/trace/ci/init.js", runSpec{Name: "reporting-only", Role: roleReporting, Instrumented: true})
		require.NoError(t, err)
		relative, err := filepath.Rel(session.Directory(), executor.temporary)
		require.NoError(t, err)
		require.True(t, filepath.IsLocal(relative))
		require.FileExists(t, filepath.Join(executor.temporary, "tracer-cache.json"))
		require.NoError(t, session.Close())
		require.NoDirExists(t, executor.temporary)
	}
}

func TestMochaNativeObserverIntegration(t *testing.T) {
	modules := os.Getenv("DDTEST_MOCHA_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_MOCHA_NODE_MODULES")
	}
	for _, mode := range []string{"serial", "parallel", "setup probe", "missing source", "alias cwd", "external child", "nested child", "entry local", "TDD", "delay probe", "ESM"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
			requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
			requireWriteFile(t, filepath.Join(root, "one.cjs"), `const assert=require('node:assert/strict');describe('math',()=>it('adds',()=>assert.equal(1+1,2)))`)
			directory := t.TempDir()
			observer := filepath.Join(t.TempDir(), "observer.cjs")
			requireWriteFile(t, observer, mochaObserver)
			args := []string{"one.cjs"}
			name := filepath.Join(root, "node_modules/.bin/mocha")
			if mode == "parallel" {
				args = append(args, "--parallel", "--jobs=2")
			}
			if mode == "setup probe" {
				requireWriteFile(t, filepath.Join(root, "setup.cjs"), `beforeEach(()=>{globalThis.probeSetupLoaded=true})`)
				requireWriteFile(t, filepath.Join(root, "probe.cjs"), `const assert=require('node:assert/strict');describe('ddtest',()=>it('validation probe',()=>assert.equal(globalThis.probeSetupLoaded,true)))`)
				args = append(args, "--file", "setup.cjs")
			}
			if mode == "missing source" {
				name = "node"
				args = []string{"runner.cjs"}
				requireWriteFile(t, filepath.Join(root, "runner.cjs"), `const exported=require('mocha');const Mocha=exported.Mocha||exported.default||exported;const mocha=new Mocha();mocha.suite.addTest(new Mocha.Test('works',()=>{}));mocha.run(failures=>{process.exitCode=failures?1:0})`)
			}
			if mode == "alias cwd" {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/unit"), 0700))
				data, err := os.ReadFile(filepath.Join(root, "one.cjs"))
				require.NoError(t, err)
				requireWriteFile(t, filepath.Join(root, "packages/unit/one.cjs"), string(data))
				requireWriteFile(t, filepath.Join(root, "hook.cjs"), `require('node:fs').accessSync('hook.cjs')`)
				requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"pretest":"node hook.cjs","test":"cd packages/unit && mocha one.cjs","posttest":"node hook.cjs"}}`)
				entries := onboard.ValidationCommands(root, "npm test", "mocha")
				require.Len(t, entries, 1)
				require.Equal(t, ".", entries[0].Directory)
				words, err := shellquote.Split(entries[0].Command)
				require.NoError(t, err)
				name, args = words[0], words[1:]
			}
			if mode == "external child" || mode == "nested child" {
				helper := `require('node:child_process').execFileSync(process.execPath,['-e','console.log("helper")'],{cwd:require('node:os').tmpdir()})`
				if mode == "nested child" {
					requireWriteFile(t, filepath.Join(root, "nested.cjs"), `const exported=require('mocha');const Mocha=exported.Mocha||exported.default||exported;const runner=new Mocha();runner.suite.addTest(new Mocha.Test('nested',()=>{process.exit(7)}));runner.run()`)
					helper = `const child=require('node:child_process').spawnSync(process.execPath,['nested.cjs']);require('node:assert/strict').equal(child.status,7)`
				}
				requireWriteFile(t, filepath.Join(root, "one.cjs"), `describe('math',()=>it('adds',()=>{`+helper+`}))`)
			}
			if mode == "entry local" {
				require.NoError(t, os.Remove(filepath.Join(root, "node_modules")))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/unit"), 0700))
				require.NoError(t, os.Symlink(modules, filepath.Join(root, "packages/unit/node_modules")))
				requireWriteFile(t, filepath.Join(root, "packages/unit/runner.cjs"), `const exported=require('mocha');const Mocha=exported.Mocha||exported.default||exported;const m=new Mocha();m.addFile('one.cjs');m.run(n=>{process.exitCode=n?1:0})`)
				name = "node"
				args = []string{"packages/unit/runner.cjs"}
			}
			if mode == "TDD" {
				requireWriteFile(t, filepath.Join(root, "one.cjs"), `suite('math',()=>test('adds',()=>{}))`)
				args = append(args, "--ui=tdd")
			}
			if mode == "ESM" {
				requireWriteFile(t, filepath.Join(root, "one.mjs"), `import assert from 'node:assert/strict';describe('math',()=>it('adds',()=>assert.equal(1+1,2)))`)
				args = []string{"one.mjs"}
			}
			if mode == "delay probe" {
				requireWriteFile(t, filepath.Join(root, "probe.cjs"), mochaProbeSource)
				args = append(args, "--delay")
			}
			command := exec.CommandContext(t.Context(), name, args...)
			command.Dir = root
			command.Env = append(os.Environ(), "NODE_OPTIONS=--require="+strconv.Quote(observer), "DDTEST_MOCHA_RESULTS="+directory)
			if mode == "setup probe" || mode == "delay probe" {
				command.Env = append(command.Env, "DDTEST_MOCHA_PROBE="+filepath.Join(root, "probe.cjs"))
			}
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			var run validationRun
			readMochaResults(directory, &run)
			if mode == "missing source" {
				require.Contains(t, run.ResultError, "source file")
			} else {
				require.Empty(t, run.ResultError)
			}
			require.Empty(t, run.SuiteErrors)
			require.Len(t, run.Tests, 1)
			require.Equal(t, "passed", run.Tests[0].Status)
			want := "math adds"
			if mode == "setup probe" || mode == "delay probe" {
				want = probeName
			}
			if mode == "missing source" {
				want = "works"
			}
			require.Equal(t, want, run.Tests[0].Name)
		})
	}
}

func TestMochaCoverageGateRequiresRealSuiteEvidence(t *testing.T) {
	for _, covered := range []int{0, 1} {
		run := validationRun{framework: "mocha", Name: "reporting-only", Role: roleReporting, Instrumented: true, Facts: intake.Facts{TestCount: 1, CoveredTestCount: covered}}
		summary := run.summary()
		require.NotNil(t, summary.Coverage)
		result := completeValidationFixture("mocha")
		result.Runs = append(result.Runs, summary)
		require.Equal(t, covered == 0, missingRealCoverage(result))
	}
	probe := validationRun{framework: "mocha", Name: "skipping", ProbeMode: "fail", Instrumented: true, Facts: intake.Facts{TestCount: 1}}
	require.Nil(t, probe.summary().Coverage, "synthetic probes are not real-source coverage evidence")
}

func TestCoverageBlockerUsesTheSelectedFramework(t *testing.T) {
	for _, name := range []string{"jest", "vitest", "mocha"} {
		result := validationResult{Framework: name, Compatibility: verdict{Status: "compatible"}, Runs: []runSummary{{Coverage: &coverageSummary{Status: "not observed"}}}}
		summary := summarizeValidation(result)
		require.Contains(t, strings.Join(summary.BlockingChecks, "\n"), displayName(name)+" real-suite coverage")
	}
}

type mochaEnvironmentExecutor struct{ envs []map[string]string }

func (e *mochaEnvironmentExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	return e.CombinedOutputInDirectory(ctx, "", name, args, env)
}

func (e *mochaEnvironmentExecutor) CombinedOutputInDirectory(_ context.Context, _ string, _ string, _ []string, env map[string]string) ([]byte, error) {
	e.envs = append(e.envs, env)
	if results := env["DDTEST_MOCHA_RESULTS"]; results != "" {
		return nil, os.WriteFile(filepath.Join(results, "native.json"), []byte(`{"complete":true,"tests":[]}`), 0600)
	}
	return []byte(`{"version":"11.7.6","watch":false}`), nil
}

func TestMochaRunsKeepCIPathAndScriptAssignments(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.framework = framework.NewMocha(platform.NewJavaScript())
	writeMochaManifest(t, drive.repositoryRoot, `{"scripts":{"test":"MODE=unit mocha one.cjs"},"devDependencies":{"mocha":"11.7.6"}}`)
	drive.command, drive.args = "npm", []string{"test"}
	drive.configurationEnvironment = map[string]string{"PATH": "/ci/tools:/usr/bin"}
	executor := &mochaEnvironmentExecutor{}
	drive.executor = executor
	drive.startIntake = func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }

	_ = drive.checkMochaPreflight(t.Context(), io.Discard, &validationResult{CIRuntime: &onboard.RuntimeCheck{}})
	require.NotEmpty(t, executor.envs)
	require.Equal(t, "unit", executor.envs[0]["MODE"], "preflight loads .mocharc with the script's assignments")

	session, err := workspace.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	_, err = drive.runMocha(t.Context(), io.Discard, session, "/trace/ci/init.js", runSpec{Name: "reporting-only", Role: roleReporting, Instrumented: true})
	require.NoError(t, err)
	path := executor.envs[len(executor.envs)-1]["PATH"]
	require.True(t, strings.HasSuffix(path, string(os.PathListSeparator)+"/ci/tools:/usr/bin"), path)
}

func writeMochaManifest(t *testing.T, root, manifest string) {
	t.Helper()
	requireWriteFile(t, filepath.Join(root, "package.json"), manifest)
}

func TestMochaNestedGuardDisablesTracingOnlyForNestedCLIs(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required")
	}
	directory := t.TempDir()
	guard := filepath.Join(directory, "guard.cjs")
	requireWriteFile(t, guard, mochaNestedGuard)
	print := "console.log([process.env.DD_TRACE_ENABLED, process.env.DD_CIVISIBILITY_ENABLED, process.env.DD_TRACE_DISABLED_PLUGINS || '-', process.env.DD_TRACE_DISABLED_INSTRUMENTATIONS || '-'].join(' '))\n"
	worker := filepath.Join(directory, "node_modules", "mocha", "lib", "nodejs", "worker.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(worker), 0700))
	for _, file := range []string{filepath.Join(directory, "mocha.js"), filepath.Join(directory, "runner.cjs"), worker} {
		requireWriteFile(t, file, print)
	}
	run := func(entry, owner string, env ...string) string {
		cmd := exec.CommandContext(t.Context(), node, "--require", guard, entry)
		cmd.Env = append(append(os.Environ(), "DD_TRACE_ENABLED=true", "DD_CIVISIBILITY_ENABLED=true", "DDTEST_MOCHA_OWNER="+owner), env...)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		return strings.TrimSpace(string(output))
	}
	// This test process stands in for the owner that forks parallel workers.
	parent := strconv.Itoa(os.Getpid())
	require.Equal(t, "false false - -", run(filepath.Join(directory, "mocha.js"), "1"), "a Mocha CLI under another owner is nested")
	require.Equal(t, "true true - -", run(filepath.Join(directory, "mocha.js"), ""), "the first Mocha CLI keeps tracing")
	require.Equal(t, "true true - -", run(worker, parent), "the owner's parallel workers keep tracing")
	require.Equal(t, "true true mocha mocha", run(worker, "1"), "a nested runner's workers don't report the suite")
	require.Equal(t, "true true mocha mocha", run(filepath.Join(directory, "runner.cjs"), "1"), "a programmatic runner keeps tracing everything except Mocha")
	require.Equal(t, "true true express,mocha mocha", run(filepath.Join(directory, "runner.cjs"), "1", "DD_TRACE_DISABLED_PLUGINS=express"), "customer exclusions are kept")
}

func TestMochaMissingCoverageNamesItsCause(t *testing.T) {
	for _, tc := range []struct {
		wrapper       string
		parallel, esm bool
		want          string
	}{
		{"nyc", true, false, "parallel mode"},
		{"c8", false, false, "c8 collects V8 coverage"},
		{"nyc", false, true, "Native ESM coverage"},
		{"", false, false, "does not run under nyc"},
		{"nyc", false, false, "nyc ran, but no test reported coverage"},
	} {
		require.Contains(t, mochaCoverageCause(tc.wrapper, tc.parallel, tc.esm), tc.want)
	}
	run := validationRun{framework: "mocha", Name: "reporting-only", Role: roleReporting, Instrumented: true, coverageCause: mochaCoverageCause("nyc", true, false), Facts: intake.Facts{TestCount: 2}}
	result := validationResult{Framework: "mocha", Runs: []runSummary{run.summary()}}
	require.Contains(t, run.summary().Coverage.Reason, "parallel mode")
	summary := summarizeValidation(result)
	require.Contains(t, strings.Join(summary.BlockingChecks, "\n"), "Mocha real-suite coverage was not observed: Coverage reported for 0/2 real tests; synthetic probes are excluded. Mocha parallel mode")
	require.NotContains(t, strings.Join(summary.BlockingChecks, "\n"), "review NYC/ESM instrumentation")
}

func TestMochaDelayWithoutRunIsNamed(t *testing.T) {
	directory := t.TempDir()
	requireWriteFile(t, filepath.Join(directory, "1-0.json"), `{"complete":false,"delayed":true,"tests":[],"errors":[]}`)
	var run validationRun
	readMochaResults(directory, &run)
	require.Contains(t, run.ResultError, "delay mode waits for a global run() call")
	verdict := compareJest(run, testRun("passed", 0))
	require.Contains(t, verdict.Reason, "delay mode waits for a global run() call")
}

func writeFakeTracer(t *testing.T, modules, version string) string {
	t.Helper()
	tracer := filepath.Join(modules, "dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci"), 0700))
	requireWriteFile(t, filepath.Join(tracer, "package.json"), `{"name":"dd-trace","version":"`+version+`","engines":{"node":">=18"}}`)
	requireWriteFile(t, filepath.Join(tracer, "ci", "init.js"), "")
	return filepath.Join(tracer, "ci", "init.js")
}

func TestMochaPreflightSelectsTheTracerOfTheCIEnvironment(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	drive := preparedTestdrive(t)
	drive.framework = framework.NewMocha(platform.NewJavaScript())
	drive.platform = platform.NewJavaScript()
	writeMochaManifest(t, drive.repositoryRoot, `{"scripts":{"test":"mocha one.cjs"},"devDependencies":{"mocha":"11.7.6"}}`)
	writeFakeTracer(t, filepath.Join(drive.repositoryRoot, "node_modules"), "5.132.0")
	// The developer's shell preloads another tracer; the CI step clears NODE_OPTIONS.
	t.Setenv("NODE_OPTIONS", "--require "+writeFakeTracer(t, filepath.Join(t.TempDir(), "node_modules"), "6.21.0"))
	drive.configurationEnvironment = map[string]string{"NODE_OPTIONS": ""}
	drive.command, drive.args = "npm", []string{"test"}
	drive.executor = &mochaEnvironmentExecutor{}
	result := validationResult{Framework: "mocha", CIRuntime: &onboard.RuntimeCheck{}}
	_ = drive.checkMochaPreflight(t.Context(), io.Discard, &result)
	require.NotNil(t, result.Selection)
	require.Equal(t, "project", result.Selection.Source)
	require.Equal(t, "5.132.0", result.Selection.Version)
}

func TestMochaPreflightDefersGitTracersToInstallation(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	for _, checkOnly := range []bool{false, true} {
		drive := preparedTestdrive(t)
		drive.framework = framework.NewMocha(platform.NewJavaScript())
		drive.platform = platform.NewJavaScript()
		writeMochaManifest(t, drive.repositoryRoot, `{"scripts":{"test":"mocha one.cjs"},"devDependencies":{"mocha":"11.7.6"}}`)
		drive.command, drive.args = "npm", []string{"test"}
		drive.executor = &mochaEnvironmentExecutor{}
		drive.tracerVersion, drive.checkOnly = "git:https://github.com/DataDog/dd-trace-js.git#master", checkOnly
		result := validationResult{Framework: "mocha", CIRuntime: &onboard.RuntimeCheck{}}
		err := drive.checkMochaPreflight(t.Context(), io.Discard, &result)
		require.Empty(t, result.Selection.Version, "a git: fallback has no version before installation")
		if checkOnly {
			require.ErrorContains(t, err, "mocha preflight", "check-only cannot install, so it stays inconclusive")
		} else {
			require.NoError(t, err, "full runs check the installed build after installation")
		}
	}
}
