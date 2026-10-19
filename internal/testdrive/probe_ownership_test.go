package testdrive

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProbeRecoveryAfterKilledProcess(t *testing.T) {
	if root := os.Getenv("DDTEST_PROBE_CRASH_HELPER"); root != "" {
		lock, err := acquireWorkflowLock(root)
		require.NoError(t, err)
		defer func() { _ = lock.Close() }()
		probe, err := createProbe(root, validationRun{Tests: []jestTest{{File: filepath.Join(root, "real.test.js"), Status: "passed"}}}, "owned test")
		require.NoError(t, err)
		fmt.Println(probe)
		time.Sleep(time.Minute) // Parent kills us; no deferred cleanup can run.
		return
	}
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "real.test.js"), "customer test")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProbeRecoveryAfterKilledProcess$")
	child.Env = append(os.Environ(), "DDTEST_PROBE_CRASH_HELPER="+root)
	stdout, err := child.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	probe := strings.TrimSpace(line)
	require.FileExists(t, probe)
	_, err = acquireWorkflowLock(root)
	require.ErrorIs(t, err, errWorkflowBusy, "an active owner must prevent startup recovery")
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
	lock, err := acquireWorkflowLock(root)
	require.NoError(t, err, "the OS releases the killed owner's lock")
	defer func() { require.NoError(t, lock.Close()) }()
	require.NoError(t, recoverProbes(root))
	require.NoFileExists(t, probe)
	require.FileExists(t, filepath.Join(root, "real.test.js"))
}

func TestProbeRecoveryRequiresExactOwnershipAndUnchangedContents(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "abandoned", true: "modified"}[changed], func(t *testing.T) {
			root := t.TempDir()
			requireWriteFile(t, filepath.Join(root, "real.test.js"), "customer test")
			lookalike := filepath.Join(root, "ddtest-customer.test.js")
			requireWriteFile(t, lookalike, "customer fixture")
			probe, err := createProbe(root, validationRun{Tests: []jestTest{{File: filepath.Join(root, "real.test.js"), Status: "passed"}}}, "owned test")
			require.NoError(t, err)
			// No normal cleanup: emulate the files left by a killed process.
			if changed {
				requireWriteFile(t, probe, "customer edited this file")
			}
			lock, err := acquireWorkflowLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, lock.Close()) })
			err = recoverProbes(root)
			if changed {
				require.ErrorContains(t, err, "was modified")
				data, err := os.ReadFile(probe)
				require.NoError(t, err)
				require.Equal(t, "customer edited this file", string(data))
				// Restore only our fixture so its ownership record can be removed.
				requireWriteFile(t, probe, "owned test")
				require.NoError(t, recoverProbes(root))
			} else {
				require.NoError(t, err)
			}
			require.NoFileExists(t, probe)
			data, err := os.ReadFile(lookalike)
			require.NoError(t, err)
			require.Equal(t, "customer fixture", string(data))
			require.NoError(t, recoverProbes(root), "recovery is idempotent")
		})
	}
}

func TestOwnedProbeUpdatesRemainRecoverable(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "real.cy.js")
	requireWriteFile(t, source, "customer test")
	probe, err := createProbe(root, validationRun{Tests: []jestTest{{File: source, Status: "passed"}}}, "")
	require.NoError(t, err)
	require.NoError(t, writeOwnedProbe(root, probe, []byte("updated Cypress scenario")))
	require.NoError(t, removeProbe(root, probe))
	directory, err := probeOwnershipDirectory(root)
	require.NoError(t, err)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}
