package testdrive

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

func TestProbeRecoverySurvivesTemporaryDirectoryChange(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "real.test.js")
	requireWriteFile(t, source, "customer test")
	probe, err := createProbe(root, validationRun{Tests: []jestTest{{File: source, Status: "passed"}}}, "owned test")
	require.NoError(t, err)
	t.Setenv("TMPDIR", t.TempDir())
	require.NoError(t, recoverProbes(root))
	require.NoFileExists(t, probe)
	require.NoDirExists(t, filepath.Join(root, ".testoptimization", ".probes"))
}

func TestProbeCreationNeverReplacesCustomerFiles(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "customer.test.js")
	requireWriteFile(t, probe, "customer contents")
	require.Error(t, createOwnedProbe(root, probe, []byte("probe contents")))
	require.NoError(t, recoverProbes(root))
	data, err := os.ReadFile(probe)
	require.NoError(t, err)
	require.Equal(t, "customer contents", string(data))
}

func TestUnpublishedProbeContentsAreRecovered(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "probe.test.js")
	contents := []byte("complete contents")
	peer, err := probeContentsFile(root, contents)
	require.NoError(t, err)
	require.NoError(t, writeProbeRecord(probeOwnership{Root: canonicalWorkflowRoot(root), Path: probe, Hash: fmt.Sprintf("%x", sha256.Sum256(contents)), Peer: peer}))
	directory, err := probeOwnershipDirectory(root)
	require.NoError(t, err)
	requireWriteFile(t, filepath.Join(directory, "contents-interrupted"), "partial")
	require.NoError(t, recoverProbes(root))
	require.NoFileExists(t, probe)
	require.NoDirExists(t, directory)
}

func TestProbeOwnershipRejectsCheckoutRecordsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".testoptimization", ".probes")
	require.NoError(t, os.MkdirAll(directory, 0755))
	_, err := probeOwnershipDirectory(root)
	require.Error(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".testoptimization")))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, ".testoptimization")))
	_, err = probeOwnershipDirectory(root)
	require.Error(t, err)
}

func TestProbePublicationExplainsUnsupportedFilesystem(t *testing.T) {
	err := probePublicationError(&os.LinkError{Op: "link", Old: "private-contents", New: "probe.test.js", Err: syscall.EXDEV})
	require.ErrorIs(t, err, errProbePublicationUnsupported)
	require.ErrorIs(t, err, syscall.EXDEV)
	require.ErrorContains(t, err, "same filesystem as .testoptimization/.probes")
	err = probePublicationError(os.ErrExist)
	require.NotErrorIs(t, err, errProbePublicationUnsupported)
	require.ErrorIs(t, err, os.ErrExist)
}

func TestProbeRecoveryPreservesRecordWhenPeerIsMissing(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "probe.test.js")
	require.NoError(t, createOwnedProbe(root, probe, []byte("owned")))
	path, err := probeOwnershipPath(root, probe)
	require.NoError(t, err)
	record, err := readProbeOwnership(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(record.Peer))
	require.Error(t, recoverProbes(root))
	require.FileExists(t, probe)
	require.FileExists(t, path, "missing identity evidence must not silently orphan a published probe")
}

func TestAbandonedCypressProbeIsSkipped(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	script := `let skipped=0;global.Cypress={env:()=>undefined};global.describe=(name,body)=>body();global.it=()=>{throw new Error('unexpected active probe')};global.it.skip=()=>skipped++;` + cypressProbeSource + `;require('node:assert').equal(skipped,1);`
	output, err := exec.CommandContext(t.Context(), node, "-e", script).CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestUnpublishedProbeCannotClaimExistingSameContents(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "customer.test.js")
	requireWriteFile(t, probe, "same contents")
	peer, err := probeContentsFile(root, []byte("same contents"))
	require.NoError(t, err)
	record := probeOwnership{Root: canonicalWorkflowRoot(root), Path: probe, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte("same contents"))), Peer: peer}
	require.NoError(t, writeProbeRecord(record))
	// Emulate death after recording ownership but before the exclusive link.
	require.Error(t, recoverProbes(root))
	data, err := os.ReadFile(probe)
	require.NoError(t, err)
	require.Equal(t, "same contents", string(data))
}
