// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package cligit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/impactedtests"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
)

type fakeClient struct {
	mu                          sync.Mutex
	searches, uploads, settings int
	impacted                    bool
}

func (c *fakeClient) GetSettings() (*net.SettingsResponseData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settings++
	return &net.SettingsResponseData{ImpactedTestsEnabled: c.impacted}, nil
}

func (c *fakeClient) GetCommits([]string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.searches++
	return nil, nil
}

func (c *fakeClient) SendPackFiles(string, []string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.uploads++
	return 42, nil
}

func (c *fakeClient) counts() (searches, uploads int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.searches, c.uploads
}

// usedState returns a state directory that a test process has used.
func usedState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, utils.CLIGitUsedFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// gitRepository makes the current directory a one-commit repository.
func gitRepository(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Worker", "-c", "user.email=worker@example.invalid", "commit", "--allow-empty", "-m", "Worker"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestRunUploadsOnlyWhenTold(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	fake := &fakeClient{}
	original := newClient
	newClient = func() client { return fake }
	t.Cleanup(func() { newClient = original })

	stopped := t.TempDir()
	if err := Run(stopped, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	var record utils.CLIGitRecord
	if err := utils.ReadCLIGitStateFile(stopped, utils.CLIGitRecordFile, &record); err != nil || record.Commit.Sha == "" {
		t.Fatalf("record %+v, %v", record, err)
	}
	if searches, _ := fake.counts(); searches != 0 {
		t.Fatalf("worker without a go-ahead searched %d times", searches)
	}
	if _, err := os.Stat(filepath.Join(stopped, utils.CLIGitUploadFile)); !os.IsNotExist(err) {
		t.Fatalf("worker without a go-ahead recorded an upload: %v", err)
	}

	utils.ResetHandedCLIGitStateForTesting()
	// Told to proceed, a worker that no test process used searches ahead,
	// a read-only request, but does not upload.
	unusedClient := &fakeClient{}
	newClient = func() client { return unusedClient }
	unused := t.TempDir()
	if err := Run(unused, strings.NewReader(Proceed+"\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(unused, utils.CLIGitUploadFile)); !os.IsNotExist(err) {
		t.Fatalf("unused worker recorded an upload: %v", err)
	}
	if _, uploads := unusedClient.counts(); uploads != 0 {
		t.Fatalf("unused worker uploaded %d pack files", uploads)
	}
	newClient = func() client { return fake }

	utils.ResetHandedCLIGitStateForTesting()
	proceeded := usedState(t)
	if err := Run(proceeded, strings.NewReader(Proceed+"\n")); err != nil {
		t.Fatal(err)
	}
	var upload utils.CLIGitUpload
	if err := utils.ReadCLIGitStateFile(proceeded, utils.CLIGitUploadFile, &upload); err != nil || upload.Bytes != 42 || upload.Error != "" {
		t.Fatalf("upload %+v, %v", upload, err)
	}
	if searches, uploads := fake.counts(); searches != 1 || uploads != 1 {
		t.Fatalf("searches %d, uploads %d", searches, uploads)
	}
	var state impactedtests.CLIState
	if err := utils.ReadCLIGitStateFile(proceeded, utils.CLIGitImpactedFile, &state); err != nil || state.Computed || state.Reason == "" {
		t.Fatalf("impacted tests disabled by the settings: %+v, %v", state, err)
	}

	// With impacted tests enabled, the worker computes the analyzer's input.
	utils.ResetHandedCLIGitStateForTesting()
	fake.impacted = true
	enabled := usedState(t)
	if err := Run(enabled, strings.NewReader(Proceed+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := utils.ReadCLIGitStateFile(enabled, utils.CLIGitImpactedFile, &state); err != nil || !state.Computed || state.CurrentCommit == "" {
		t.Fatalf("impacted tests enabled: %+v, %v", state, err)
	}

	utils.ResetHandedCLIGitStateForTesting()
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	disabled := usedState(t)
	if err := Run(disabled, strings.NewReader(Proceed+"\n")); err != nil {
		t.Fatal(err)
	}
	if searches, _ := fake.counts(); searches != 2 {
		t.Fatalf("disabled upload searched: %d", searches)
	}
	if err := utils.ReadCLIGitStateFile(disabled, utils.CLIGitUploadFile, &upload); err != nil || upload.Error == "" {
		t.Fatalf("disabled upload %+v, %v", upload, err)
	}
}
