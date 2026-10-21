//go:build darwin || linux

package testdrive

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlaywrightCleanupStopsOnlyLiveOwnedDescendants(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed for the process lifecycle regression")
	}
	for _, mode := range []string{"node", "non-node", "non-detached", "exec-file", "fork-custom-env", "owner-crash"} {
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
			if mode == "non-node" || mode == "non-detached" || mode == "exec-file" {
				python, err := exec.LookPath("python3")
				if err != nil {
					t.Skip("Python is needed for the non-Node group regression")
				}
				script := fmt.Sprintf("import socket,pathlib; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(); pathlib.Path(%q).write_text(str(s.getsockname()[1])); __import__('time').sleep(60)", portFile)
				parent = fmt.Sprintf(`const child=require('node:child_process').spawn(%q,['-c',%q],{detached:%t,stdio:'ignore'});child.unref();`, python, script, mode == "non-node")
				if mode == "exec-file" {
					parent = fmt.Sprintf(`const child=require('node:child_process').execFile(%q,['-c',%q],{stdio:'ignore'});child.stdout.destroy();child.stderr.destroy();child.unref();`, python, script)
				}
			}
			if mode == "fork-custom-env" {
				childPath := filepath.Join(dir, "fork.cjs")
				require.NoError(t, os.WriteFile(childPath, []byte(child+`if(process.env.PARENT_ONLY)process.exit(1);process.send('ready');process.disconnect();`), 0600))
				parent = fmt.Sprintf(`process.env.PARENT_ONLY='must-not-leak';const c=require('node:child_process').fork(%q,[],{detached:true,stdio:'ignore',env:{}});c.on('message',()=>c.unref());`, childPath)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "-e", parent)
			cmd.Env = append(os.Environ(), "NODE_OPTIONS=--require "+filepath.Join(dir, "children.cjs"), "DDTEST_PROCESS_OWNER="+env["DDTEST_PROCESS_OWNER"])
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			var port []byte
			require.Eventually(t, func() bool { port, err = os.ReadFile(portFile); return err == nil }, 5*time.Second, 20*time.Millisecond)
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
