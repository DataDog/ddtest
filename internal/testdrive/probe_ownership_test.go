package testdrive

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestProbeRecoveryAfterKilledProcess(t *testing.T) {
	if root := os.Getenv("DDTEST_PROBE_CRASH_HELPER"); root != "" {
		lock, err := workspace.AcquireLock(root)
		require.NoError(t, err)
		defer func() { _ = lock.Close() }()
		session := workspace.PlanSession()
		session.SetOwnerRoot(root)
		require.NoError(t, session.Create())
		requireWriteFile(t, filepath.Join(root, "abandoned-session"), session.Directory())
		configuration, err := os.MkdirTemp("", "ddtest-configuration-")
		require.NoError(t, err)
		require.NoError(t, workspace.MarkStorage(configuration, root))
		require.NoError(t, workspace.Copy(t.Context(), root, filepath.Join(configuration, "checkout"), skipIgnoredOutput))
		requireWriteFile(t, filepath.Join(root, "abandoned-configuration"), configuration)
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
	_, err = workspace.AcquireLock(root)
	require.ErrorIs(t, err, workspace.ErrBusy, "an active owner must prevent startup recovery")
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
	lock, err := workspace.AcquireLock(root)
	require.NoError(t, err, "the OS releases the killed owner's lock")
	defer func() { require.NoError(t, lock.Close()) }()
	session, err := os.ReadFile(filepath.Join(root, "abandoned-session"))
	require.NoError(t, err)
	require.DirExists(t, string(session))
	require.NoError(t, workspace.RecoverSessions(root, &bytes.Buffer{}))
	require.NoDirExists(t, string(session))
	configuration, err := os.ReadFile(filepath.Join(root, "abandoned-configuration"))
	require.NoError(t, err)
	require.NoDirExists(t, string(configuration), "startup recovery reclaims the killed owner’s marked configuration copy")
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
			lock, err := workspace.AcquireLock(root)
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
	require.NoError(t, writeProbeRecord(probeOwnership{Root: workspace.CanonicalRoot(root), Path: probe, Hash: fmt.Sprintf("%x", sha256.Sum256(contents)), Peer: peer}))
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
	for _, cause := range []error{syscall.EXDEV, syscall.ENOTSUP, syscall.ENOSYS, errors.ErrUnsupported} {
		err := probePublicationError(&os.LinkError{Op: "link", Old: "private-contents", New: "probe.test.js", Err: cause})
		require.ErrorIs(t, err, errProbePublicationUnsupported)
		require.ErrorIs(t, err, cause)
		require.ErrorContains(t, err, "same filesystem as .testoptimization/.probes")
	}
	err := probePublicationError(os.ErrExist)
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

func TestUnpublishedProbeCannotClaimExistingSameContents(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "customer.test.js")
	requireWriteFile(t, probe, "same contents")
	peer, err := probeContentsFile(root, []byte("same contents"))
	require.NoError(t, err)
	record := probeOwnership{Root: workspace.CanonicalRoot(root), Path: probe, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte("same contents"))), Peer: peer}
	require.NoError(t, writeProbeRecord(record))
	// Emulate death after recording ownership but before the exclusive link.
	require.NoError(t, recoverProbes(root))
	require.NoFileExists(t, peer)
	data, err := os.ReadFile(probe)
	require.NoError(t, err)
	require.Equal(t, "same contents", string(data))
}

func TestRecoveryPreservesReplacedProbeAndDoesNotBlockLaterRuns(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "probe.test.js")
	require.NoError(t, createOwnedProbe(root, probe, []byte("owned")))
	replacement := filepath.Join(root, "replacement")
	requireWriteFile(t, replacement, "owned")
	require.NoError(t, os.Rename(replacement, probe))
	var output bytes.Buffer
	require.NoError(t, recoverProbesWithOutput(root, &output))
	require.FileExists(t, probe)
	require.Contains(t, output.String(), "kept probe.test.js because ddtest cannot prove it is its own probe")
	require.Contains(t, output.String(), "delete it if it is a leftover probe")
	require.NotContains(t, output.String(), "Recovered probe ownership", "a kept file was not recovered")
	require.NoError(t, recoverProbes(root))
}

func TestStartupRecoveryFailureReplacesEarlierSuccess(t *testing.T) {
	run := preflightFixture(t, "30.2.0", "jest-circus/runner")
	require.NoError(t, finishValidation(&bytes.Buffer{}, run.repositoryRoot, pairedReport("old success", "passed")))
	probe := filepath.Join(run.repositoryRoot, "probe.test.js")
	require.NoError(t, createOwnedProbe(run.repositoryRoot, probe, []byte("owned")))
	requireWriteFile(t, probe, "edited by customer")
	for _, checkOnly := range []bool{false, true} {
		run.checkOnly = checkOnly
		var output bytes.Buffer
		require.ErrorContains(t, run.Run(t.Context(), &output), "move or delete")
		report := readValidationReport(t, run.repositoryRoot)
		require.False(t, report.Success)
		require.NotEmpty(t, report.Error)
		if checkOnly {
			require.Equal(t, "recovery_failed", report.Preflight.Verdict.Code)
		} else {
			require.Equal(t, "recovery_failed", report.Compatibility.Code)
		}
		require.FileExists(t, probe)
	}
}

func TestCheckOnlyAndLegacyRunsRecoverAbandonedProbes(t *testing.T) {
	for _, checkOnly := range []bool{true, false} {
		run := preflightFixture(t, "30.2.0", "jest-circus/runner")
		probe := filepath.Join(run.repositoryRoot, "probe.test.js")
		require.NoError(t, createOwnedProbe(run.repositoryRoot, probe, []byte("owned")))
		if checkOnly {
			run.checkOnly = true
		} else {
			run.framework = &telemetryOnlyFramework{}
			run.preflight = nil
			run.platform = &fakeTracer{preloadPath: "/trace/ci/init.js"}
			run.executor = &fakeTestdriveExecutor{}
			run.startIntake = func(string, intake.Scenario) (localIntake, error) {
				return &fakeIntake{findings: intake.Facts{TestEventCount: 1}}, nil
			}
		}
		var output bytes.Buffer
		require.NoError(t, run.Run(t.Context(), &output))
		require.NoFileExists(t, probe)
		require.Contains(t, output.String(), "Recovered probe ownership: probe.test.js")
	}
}
