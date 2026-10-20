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
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
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
	requireWriteFile(t, filepath.Join(root, "node_modules/mocha/lib/mocha.js"), `module.exports=class Mocha { constructor(){this.options={ui:"bdd",timeout:1234,reporter:"custom"};this.files=["original.js"];this.suite={retries:n=>this.retryCount=n}} loadFiles(){return this.files} run(fn){fn(this);return this} }`)
	observer := filepath.Join(root, "observer.cjs")
	requireWriteFile(t, observer, mochaObserver)
	requireWriteFile(t, filepath.Join(root, "run.cjs"), `
const assert=require('node:assert/strict'); const Runner=require('mocha/lib/runner');
const Mocha=require('mocha/lib/mocha'); const mocha=new Mocha();
assert.deepEqual(mocha.loadFiles(),[process.env.DDTEST_MOCHA_PROBE]);
assert.equal(mocha.retryCount,0); assert.equal(mocha.options.timeout,1234);
assert.equal(mocha.options.reporter,'custom');
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
		session, err := NewSession()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close()) })
		_, err = drive.runMocha(t.Context(), io.Discard, session, "/trace/ci/init.js", "reporting-only", true, intake.Scenario{}, "", "")
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
	for _, mode := range []string{"serial", "parallel", "setup probe", "missing source", "alias cwd"} {
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
				requireWriteFile(t, filepath.Join(root, "runner.cjs"), `const Mocha=require('mocha');const mocha=new Mocha();mocha.suite.addTest(new Mocha.Test('works',()=>{}));mocha.run(failures=>{process.exitCode=failures?1:0})`)
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
			command := exec.CommandContext(t.Context(), name, args...)
			command.Dir = root
			command.Env = append(os.Environ(), "NODE_OPTIONS=--require="+strconv.Quote(observer), "DDTEST_MOCHA_RESULTS="+directory)
			if mode == "setup probe" {
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
			if mode == "setup probe" {
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
		run := validationRun{framework: "mocha", Name: "reporting-only", Instrumented: true, Facts: intake.Facts{TestCount: 1, CoveredTestCount: covered}}
		summary := run.summary()
		require.NotNil(t, summary.Coverage)
		result := completeValidationFixture("mocha")
		result.Runs = append(result.Runs, summary)
		require.Equal(t, covered == 0, missingRealCoverage(result))
	}
	probe := validationRun{framework: "mocha", Name: "skipping", ProbeMode: "fail", Instrumented: true, Facts: intake.Facts{TestCount: 1}}
	require.Nil(t, probe.summary().Coverage, "synthetic probes are not real-source coverage evidence")
}
