// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package impactedtests

import (
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	logger "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// CLIState is the analyzer's input that ddto's Git worker computes once for
// every test process of a checkout, after the repository upload: the base
// branch discovery and the diff each process would otherwise repeat.
type CLIState struct {
	// Computed is false when the worker did not compute the state; Reason
	// says why, and test processes then compute it themselves.
	Computed      bool      `json:"computed"`
	Reason        string    `json:"reason,omitempty"`
	CurrentCommit string    `json:"current_commit,omitempty"`
	BaseCommit    string    `json:"base_commit,omitempty"`
	Files         []CLIFile `json:"files,omitempty"`
}

// CLIFile is a modified file and its modified-line bitmap.
type CLIFile struct {
	File   string `json:"file"`
	Bitmap []byte `json:"bitmap,omitempty"`
}

// cliStateWait bounds a test process's wait for the worker's state, which it
// requests after the repository upload has ended.
var cliStateWait = 30 * time.Second

// ComputeCLIState computes what NewImpactedTestAnalyzer computes with Git.
func ComputeCLIState() CLIState {
	analyzer, err := computeAnalyzer()
	if err != nil {
		return CLIState{Reason: err.Error()}
	}
	state := CLIState{Computed: true, CurrentCommit: analyzer.currentCommitSha, BaseCommit: analyzer.baseCommitSha}
	for _, file := range analyzer.modifiedFiles {
		state.Files = append(state.Files, CLIFile{File: file.file, Bitmap: file.bitmap})
	}
	return state
}

// handedAnalyzer builds the analyzer from the worker's state when ddto handed
// one for this checkout and it was computed for this process's commits.
func handedAnalyzer() (*ImpactedTestAnalyzer, bool) {
	dir, record := utils.HandedCLIGitState()
	if record == nil {
		return nil, false
	}
	var state CLIState
	if err := utils.WaitCLIGitStateFile(dir, utils.CLIGitImpactedFile, &state, cliStateWait); err != nil {
		logger.Debug("civisibility.ImpactedTests: CLI state unavailable: %s", err.Error())
		return nil, false
	}
	if !state.Computed {
		logger.Debug("civisibility.ImpactedTests: CLI state not computed: %s", state.Reason)
		return nil, false
	}
	ciTags := utils.GetCITagsReadOnly()
	currentCommitSha := ciTags[constants.GitHeadCommit]
	if currentCommitSha == "" {
		currentCommitSha = ciTags[constants.GitCommitSHA]
	}
	if currentCommitSha != state.CurrentCommit {
		return nil, false
	}
	if base := ciTags[constants.GitPrBaseCommit]; base != "" && base != state.BaseCommit {
		return nil, false
	}
	modifiedFiles := make([]fileWithBitmap, 0, len(state.Files))
	for _, file := range state.Files {
		modifiedFiles = append(modifiedFiles, fileWithBitmap{file: file.File, bitmap: file.Bitmap})
	}
	logger.Debug("civisibility.ImpactedTests: loaded from ddto [from: %s to %s]: %v", state.BaseCommit, state.CurrentCommit, modifiedFiles) //nolint:gocritic // File list debug logging
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles:    modifiedFiles,
		currentCommitSha: state.CurrentCommit,
		baseCommitSha:    state.BaseCommit,
	}
	analyzer.getPreparedModifiedFiles()
	return analyzer, true
}
