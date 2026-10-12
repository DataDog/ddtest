// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

// Package cligit is the worker that ddto's CLI starts once per invocation to do
// the Git work that every test process of a checkout would otherwise repeat.
// It runs in the invocation's directory, with the test processes' environment,
// so the SDK code it calls sees what a test process would see. It writes its
// results to a state directory that the CLI hands to the test processes; see
// utils.HandedCLIGitState.
package cligit

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/gitupload"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/impactedtests"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Proceed is the line the CLI writes to the worker's input once test
// processes will receive the state; end of input without it stops the worker.
// After it, the CLI keeps the input open until go test exits.
const Proceed = "proceed"

// client is the part of the CI Visibility client that the worker uses.
type client interface {
	gitupload.Client
	GetSettings() (*net.SettingsResponseData, error)
}

// newClient creates the CI Visibility client; tests replace it.
var newClient = func() client {
	c := net.NewClientWithoutTelemetry("")
	if c == nil {
		return nil
	}
	return c
}

// Run writes the checkout's Git record to dir, then waits for the CLI. Once
// told to proceed, it compares the local commits with the backend's while go
// test builds, a read-only request. Once a test process uses the state, it
// uploads the missing commits, as each test process would, and records the
// end of the upload; meanwhile it requests the settings, and if they enable
// impacted tests it records the base commit and modified files. When go test
// exits without running a test process that uses the state, as when every
// result comes from its cache, the worker exits without changing the
// repository or uploading.
func Run(dir string, input io.Reader) error {
	if infra.BoolEnv("DD_TRACE_DEBUG", false) {
		log.SetLevel(log.LevelDebug)
	}
	record := utils.CollectCLIGitRecord()
	if err := utils.WriteCLIGitStateFile(dir, utils.CLIGitRecordFile, record); err != nil {
		return err
	}
	utils.UseCLIGitState(dir, record)
	reader := bufio.NewReader(input)
	line, _ := reader.ReadString('\n')
	if line != Proceed+"\n" {
		return nil
	}
	c := newClient()
	uploadEnabled := infra.BoolEnv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, true)
	search := searchAhead(c, uploadEnabled)
	if !waitUsed(dir, reader) {
		return nil
	}
	// Test processes request the settings while they upload.
	settings := make(chan settingsResult, 1)
	if c != nil {
		go func() {
			response, err := c.GetSettings()
			settings <- settingsResult{response, err}
		}()
	}
	uploaded := upload(c, uploadEnabled, search)
	if err := utils.WriteCLIGitStateFile(dir, utils.CLIGitUploadFile, uploaded); err != nil {
		return err
	}
	return utils.WriteCLIGitStateFile(dir, utils.CLIGitImpactedFile, impacted(c, uploaded, settings))
}

type settingsResult struct {
	response *net.SettingsResponseData
	err      error
}

// searchAhead starts the upload's first commit search; the returned function
// waits for it.
func searchAhead(c client, uploadEnabled bool) func() (*gitupload.SearchCommitsResponse, error) {
	if c == nil || !uploadEnabled {
		return nil
	}
	var response *gitupload.SearchCommitsResponse
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err = gitupload.SearchCommits(c)
	}()
	return func() (*gitupload.SearchCommitsResponse, error) {
		<-done
		return response, err
	}
}

// upload runs the test processes' repository upload, starting from the
// commit search made ahead. Test processes that disable it do not wait for
// this result.
func upload(c client, uploadEnabled bool, search func() (*gitupload.SearchCommitsResponse, error)) utils.CLIGitUpload {
	if !uploadEnabled {
		return utils.CLIGitUpload{Error: "repository upload disabled"}
	}
	if c == nil {
		return utils.CLIGitUpload{Error: errors.New("no CI Visibility client").Error()}
	}
	hooks := gitupload.ClientHooks(c)
	repeat := hooks.GetSearchCommits
	hooks.GetSearchCommits = func() (*gitupload.SearchCommitsResponse, error) {
		hooks.GetSearchCommits = repeat
		return search()
	}
	bytes, err := gitupload.Upload(hooks)
	if err != nil {
		log.Error("civisibility: error uploading repository changes: %s", err.Error())
		return utils.CLIGitUpload{Bytes: bytes, Error: err.Error()}
	}
	log.Debug("civisibility: uploaded %d bytes in pack files", bytes)
	return utils.CLIGitUpload{Bytes: bytes}
}

// impacted computes the impacted-tests state when the settings enable it, as
// a test process does after the upload: the base branch discovery and the diff
// then need the history that the upload may have fetched.
func impacted(c client, uploaded utils.CLIGitUpload, settings <-chan settingsResult) impactedtests.CLIState {
	if c == nil {
		return impactedtests.CLIState{Reason: "no CI Visibility client"}
	}
	result := <-settings
	response, err := result.response, result.err
	if err == nil && response != nil && response.RequireGit && uploaded.Error == "" {
		response, err = c.GetSettings()
	}
	switch {
	case err != nil:
		return impactedtests.CLIState{Reason: "settings: " + err.Error()}
	case response == nil || !response.ImpactedTestsEnabled:
		return impactedtests.CLIState{Reason: "impacted tests disabled by the settings"}
	case !infra.BoolEnv(constants.CIVisibilityImpactedTestsDetectionEnabled, true):
		return impactedtests.CLIState{Reason: "impacted tests disabled by the environment"}
	}
	return impactedtests.ComputeCLIState()
}

// waitUsed waits until a test process marks the state as used, or the CLI
// closes the input because go test has exited.
func waitUsed(dir string, input io.Reader) bool {
	closed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, input)
		close(closed)
	}()
	used := filepath.Join(dir, utils.CLIGitUsedFile)
	delay := time.Millisecond
	for {
		if _, err := os.Stat(used); err == nil {
			return true
		}
		select {
		case <-closed:
			_, err := os.Stat(used)
			return err == nil
		case <-time.After(delay):
			delay = min(2*delay, 5*time.Millisecond)
		}
	}
}
