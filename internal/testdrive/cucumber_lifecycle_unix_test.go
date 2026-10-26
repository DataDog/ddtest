//go:build darwin || linux

package testdrive

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestCucumberLifecycleReplayStopsTheHelpersItsHooksStart(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(root)
	// Detached Node and non-Node helpers, as a pretest that starts a server or
	// a watcher would leave behind.
	requireWriteFile(t, filepath.Join(root, "helper.cjs"), `const {spawn} = require('node:child_process'), fs = require('node:fs')
for (const [file, command, args] of [['node.pid', process.execPath, ['-e', 'setInterval(()=>{},1000)']], ['sh.pid', '/bin/sh', ['-c', 'sleep 60']]]) {
  const child = spawn(command, args, {detached: true, stdio: 'ignore'})
  fs.writeFileSync(file, String(child.pid))
  child.unref()
}`)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"scripts":{"pretest":"node helper.cjs"}}`)
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := &Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{Timeout: 30 * time.Second}}
	finish, err := drive.startCucumberProbeLifecycle(t.Context(), io.Discard, session, []onboard.LifecycleScript{{Directory: ".", Name: "pretest"}})
	require.NoError(t, err)
	var pids []int
	for _, name := range []string{"node.pid", "sh.pid"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		require.NoError(t, err, name)
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		require.NoError(t, err, name)
		pids = append(pids, pid)
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	}
	for _, pid := range pids {
		require.NoError(t, syscall.Kill(pid, 0), "helper %d must be running while probes run", pid)
	}
	require.NoError(t, finish(nil))
	for _, pid := range pids {
		require.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil }, 3*time.Second, 50*time.Millisecond, "helper %d outlived the lifecycle owner", pid)
	}
}
