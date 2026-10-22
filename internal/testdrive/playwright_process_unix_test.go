//go:build darwin || linux

package testdrive

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestPlaywrightCleanupStopsOnlyLiveOwnedDescendants(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed for the process lifecycle regression")
	}
	for _, mode := range []string{"node", "non-node", "non-detached", "exec-file", "exec", "fork-custom-env", "ipc-wrapper", "owner-crash"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer func() { _ = listener.Close() }()
			dir := t.TempDir()
			env := map[string]string{}
			owner, err := startProcessOwner(dir, env)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, owner.Close()) })
			portFile := filepath.Join(dir, "port")
			child := fmt.Sprintf(`const fs=require('node:fs');const s=require('node:net').createServer();s.listen(0,'127.0.0.1',()=>fs.writeFileSync(%q,String(s.address().port)));`, portFile)
			parent := fmt.Sprintf(`const child=require('node:child_process').spawn(process.execPath,['-e',%q],{detached:true,stdio:'ignore'});child.unref();`, child)
			if mode == "non-node" || mode == "non-detached" || mode == "exec-file" || mode == "exec" {
				python, err := exec.LookPath("python3")
				if err != nil {
					t.Skip("Python is needed for the non-Node group regression")
				}
				script := fmt.Sprintf("import socket,pathlib; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(); pathlib.Path(%q).write_text(str(s.getsockname()[1])); __import__('time').sleep(60)", portFile)
				parent = fmt.Sprintf(`const child=require('node:child_process').spawn(%q,['-c',%q],{detached:%t,stdio:'ignore'});child.unref();`, python, script, mode == "non-node")
				if mode == "exec-file" {
					parent = fmt.Sprintf(`const child=require('node:child_process').execFile(%q,['-c',%q],{stdio:'ignore'});child.stdout.destroy();child.stderr.destroy();child.unref();`, python, script)
				}
				if mode == "exec" {
					parent = fmt.Sprintf(`const child=require('node:child_process').exec(%q);child.stdout.destroy();child.stderr.destroy();child.unref();`, shellquote.Join(python, "-c", script))
				}
			}
			if mode == "fork-custom-env" {
				childPath := filepath.Join(dir, "fork.cjs")
				require.NoError(t, os.WriteFile(childPath, []byte(child+`if(process.env.PARENT_ONLY)process.exit(1);process.send('ready');process.disconnect();`), 0600))
				parent = fmt.Sprintf(`process.env.PARENT_ONLY='must-not-leak';const c=require('node:child_process').fork(%q,[],{detached:true,stdio:'ignore',env:{}});c.on('message',()=>c.unref());`, childPath)
			}
			if mode == "ipc-wrapper" {
				childPath := filepath.Join(dir, "worker.cjs")
				require.NoError(t, os.WriteFile(childPath, []byte(child+`process.send('ready');process.disconnect();`), 0600))
				// Like xvfb-run: a non-Node launcher retains the IPC descriptor
				// while a Node descendant sends messages and remains alive.
				// Close the parent channel after the handshake: Linux shells may
				// keep their descriptor open while waiting for that descendant.
				parent = fmt.Sprintf(`const c=require('node:child_process').spawn('/bin/sh',['-c','"$@"','wrapper',process.execPath,%q],{stdio:['ignore','ignore','ignore','ipc'],env:{}});c.on('message',()=>{c.disconnect();c.unref()});`, childPath)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "-e", parent)
			cmd.Env = append(os.Environ(), "NODE_OPTIONS=--require "+filepath.Join(dir, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"])
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			var port []byte
			require.Eventually(t, func() bool { port, err = os.ReadFile(portFile); return err == nil && len(port) > 0 }, 5*time.Second, 20*time.Millisecond)
			conn, err := net.DialTimeout("tcp", "127.0.0.1:"+string(port), time.Second)
			require.NoError(t, err)
			_ = conn.Close()
			if mode == "owner-crash" {
				// Equivalent to the kernel closing ddtest's sockets after SIGKILL.
				owner.clients.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
			} else {
				require.NoError(t, owner.Close())
			}
			require.Eventually(t, func() bool {
				c, err := net.DialTimeout("tcp", "127.0.0.1:"+string(port), 50*time.Millisecond)
				if err == nil {
					_ = c.Close()
				}
				return err != nil
			}, 3*time.Second, 20*time.Millisecond)
			conn, err = net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			require.NoError(t, err)
			_ = conn.Close()
		})
	}
}

func TestDetachedGuardPreservesExtraPipesAndExitStatus(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed for the process lifecycle regression")
	}
	directory := t.TempDir()
	env := map[string]string{}
	owner, err := startProcessOwner(directory, env)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	script := `const assert=require('node:assert');
const child=require('node:child_process').spawn('/bin/sh',['-c','read -r value <&3; echo "fd:$value"; echo "$$:$PPID" >&4; exit 7'],{detached:true,stdio:['ignore','pipe','pipe','pipe','pipe']});
let output='',identity='';child.stdout.on('data',data=>output+=data);child.stdio[4].on('data',data=>identity+=data);child.stderr.pipe(process.stderr);
child.stdio[3].end('hello\n');child.on('close',code=>{assert.equal(code,7);assert.equal(output,'fd:hello\n');assert.equal(identity,child.pid+':'+process.pid+'\n')});`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", script)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--require "+filepath.Join(directory, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"])
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestInitialOwnerConnectionFailureDoesNotKillCustomerProcess(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	directory := t.TempDir()
	env := map[string]string{}
	owner, err := startProcessOwner(directory, env)
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), node, "-e", `setTimeout(() => console.log('customer code survived'), 50)`)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--require "+filepath.Join(directory, "children.cjs"), "DDTEST_PROCESS_OWNER="+filepath.Join(directory, "missing"), "DDTEST_PROCESS_ERRORS="+env["DDTEST_PROCESS_ERRORS"])
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "customer code survived")
	var failure *processSupervisionError
	require.ErrorAs(t, owner.Close(), &failure)
	require.Equal(t, "ownership_connection_failed", failure.Code)
}

func TestUnsupportedDetachedSpawnInvalidatesValidation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	directory := t.TempDir()
	env := map[string]string{}
	owner, err := startProcessOwner(directory, env)
	require.NoError(t, err)
	marker := filepath.Join(directory, "unowned-child")
	script := fmt.Sprintf(`require('node:child_process').spawn('/bin/sh',['-c',%q],{detached:true,argv0:'custom',stdio:'ignore'})`, "touch "+marker)
	command := exec.CommandContext(t.Context(), node, "-e", script)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--require "+filepath.Join(directory, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"], "DDTEST_PROCESS_ERRORS="+env["DDTEST_PROCESS_ERRORS"])
	output, err := command.CombinedOutput()
	require.Error(t, err, string(output))
	require.Equal(t, 86, command.ProcessState.ExitCode())
	require.NoFileExists(t, marker, "an unowned detached child must not be launched")
	var failure *processSupervisionError
	require.ErrorAs(t, owner.Close(), &failure)
	require.Equal(t, "unsupported_detached_spawn", failure.Code)
}

func TestSupervisedSpawnPreservesNativeArgumentsAndErrors(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	directory := t.TempDir()
	env := map[string]string{}
	owner, err := startProcessOwner(directory, env)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	shellInit := filepath.Join(directory, "customer.bash")
	requireWriteFile(t, shellInit, "echo customer-shell-init\n")
	denied := filepath.Join(directory, "not-executable")
	require.NoError(t, os.WriteFile(denied, []byte("#!/bin/sh\nexit 0\n"), 0600))
	script := `const cp=require('node:child_process');
 const cwd=process.argv[1],denied=process.argv[2];
 async function run(make){return new Promise(resolve=>{let output='';let c;
 try{c=make()}catch(e){resolve({throw:e.code});return}
 c.stdout?.on('data',b=>output+=b);c.on('error',e=>resolve({error:e.code}));
 c.on('close',code=>resolve({code,output:output.trim()}))})}
 (async()=>{const options={cwd,stdio:['ignore','pipe','pipe']};const result=[];
 for(const make of [()=>cp.spawn('/bin/pwd'),()=>cp.spawn('/bin/pwd',[]),()=>cp.spawn('/bin/pwd',options),()=>cp.spawn('/bin/pwd',[],options),()=>cp.spawn('/bin/pwd',undefined,options),()=>cp.spawn('/bin/pwd',null,options),()=>cp.spawn('/bin/pwd',42,options),()=>cp.spawn('/bin/bash',['-c','printf target']),()=>cp.spawn('ddtest-missing-executable-731'),()=>cp.spawn(denied),()=>cp.spawn('/bin/pwd',[],{cwd:cwd+'/missing'})])result.push(await run(make));
 console.log(JSON.stringify(result));})().catch(e=>{console.error(e);process.exitCode=1})`
	invoke := func(supervised bool) []byte {
		command := exec.CommandContext(t.Context(), node, "-e", script, directory, denied)
		command.Env = append(os.Environ(), "BASH_ENV="+shellInit)
		if supervised {
			command.Env = append(command.Env, "NODE_OPTIONS=--require "+filepath.Join(directory, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"], "DDTEST_PROCESS_ERRORS="+env["DDTEST_PROCESS_ERRORS"])
		}
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		return output
	}
	require.JSONEq(t, string(invoke(false)), string(invoke(true)))
}

func TestShortLivedChildrenReleaseGuardsWithoutLoadingPreloads(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	directory := t.TempDir()
	env := map[string]string{}
	owner, err := startProcessOwner(directory, env)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	marker := filepath.Join(directory, "guard-loaded-preload")
	preload := filepath.Join(directory, "marker.cjs")
	requireWriteFile(t, preload, fmt.Sprintf(`if(process.argv[1]?.endsWith('process_guard.cjs'))require('node:fs').writeFileSync(%q,'loaded')`, marker))
	script := `const cp=require('node:child_process');let remaining=24;for(let i=0;i<24;i++){const c=cp.spawn('/bin/sleep',['0.01'],{detached:true,stdio:'ignore'});c.on('close',()=>{if(--remaining===0)console.log('done')})}`
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", script)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--require "+preload+" --require "+filepath.Join(directory, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"], "DDTEST_PROCESS_ERRORS="+env["DDTEST_PROCESS_ERRORS"])
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "done")
	require.Eventually(t, func() bool {
		count := 0
		owner.clients.Range(func(_, _ any) bool { count++; return true })
		return count == 0
	}, 5*time.Second, 50*time.Millisecond)
	require.NoFileExists(t, marker)
}

func TestSupervisorToleratesDeletedPreloadsAndErrorDirectory(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	directory := t.TempDir()
	env := map[string]string{}
	owner, err := startProcessOwner(directory, env)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	script := `const fs=require('node:fs');for(const name of ['children.cjs','process_owner.cjs','process_guard.cjs'])fs.unlinkSync(process.argv[1]+'/'+name);const c=require('node:child_process').spawn(process.execPath,['-e',"console.log('late child survived')"],{stdio:'inherit'});c.on('close',code=>process.exitCode=code)`
	command := exec.CommandContext(t.Context(), node, "-e", script, directory)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--require "+filepath.Join(directory, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"], "DDTEST_PROCESS_ERRORS="+filepath.Join(directory, "missing", "errors"))
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "late child survived")
}

func TestSupervisedGuardSupportsDescriptorsAboveNine(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	directory := t.TempDir()
	env := map[string]string{}
	owner, err := startProcessOwner(directory, env)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	script := `const assert=require('node:assert');const stdio=['ignore','ignore','pipe',...Array(9).fill('pipe')];const c=require('node:child_process').spawn('/bin/bash',['-c','echo high >&11; exit 7'],{stdio});let output='';c.stdio[11].on('data',b=>output+=b);c.stderr.pipe(process.stderr);for(let i=3;i<11;i++)c.stdio[i].destroy();c.on('close',code=>{assert.equal(code,7);assert.equal(output,'high\n')});`
	command := exec.CommandContext(t.Context(), node, "-e", script)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--require "+filepath.Join(directory, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"], "DDTEST_PROCESS_ERRORS="+env["DDTEST_PROCESS_ERRORS"])
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestRecoverPlaywrightOwnershipStorageIsRepositoryScoped(t *testing.T) {
	root := t.TempDir()
	drive := &Testdrive{repositoryRoot: root}
	owner, err := drive.ownPlaywrightProcesses(t.TempDir(), map[string]string{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	unrelated, err := os.MkdirTemp(processSocketBase(), "ddtest-process-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(unrelated)) })
	require.NoError(t, markSessionStorage(unrelated, t.TempDir()))
	// Simulate kernel closing the owner's listener on a crash, then the next run.
	require.NoError(t, owner.listener.Close())
	<-owner.accepted
	require.NoError(t, recoverProcessOwners(root, io.Discard))
	require.NoDirExists(t, owner.directory)
	require.DirExists(t, unrelated)
}

func TestProcessOwnershipSupportsLongTemporaryPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), strings.Repeat("long-temp-", 12))
	require.NoError(t, os.MkdirAll(root, 0700))
	t.Setenv("TMPDIR", root)
	owner, err := newProcessOwner()
	require.NoError(t, err, "Linux and Darwin socket paths must remain short even with long TMPDIR")
	defer func() { require.NoError(t, owner.Close()) }()
	require.LessOrEqual(t, len(owner.listener.Addr().String()), 103)
}

func TestRecoverOwnershipAlsoChecksConfiguredTemporaryRoot(t *testing.T) {
	root := t.TempDir()
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	abandoned, err := os.MkdirTemp(temporary, "ddtest-process-")
	require.NoError(t, err)
	require.NoError(t, markSessionStorage(abandoned, root))
	require.NoError(t, recoverProcessOwners(root, io.Discard))
	require.NoDirExists(t, abandoned, "storage from the previous socket base must still be recovered")
}
