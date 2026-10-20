// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.
package utils

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/codeownership"
	logger "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

var (
	codeowners           *codeownership.Resolver
	codeownersLookupDone bool
	codeownersMutex      sync.Mutex
)

// GetCodeOwners shares one repository ownership decision for this process.
// Parsing, host-specific file discovery and matching live in codeownership.
func GetCodeOwners() *codeownership.Resolver {
	rules, _ := GetCodeOwnersWithStatus()
	return rules
}

// GetCodeOwnersWithStatus reports whether discovery can be cached. Missing
// files and malformed rules are stable decisions; I/O errors can be retried.
func GetCodeOwnersWithStatus() (*codeownership.Resolver, bool) {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()
	if codeownersLookupDone {
		return codeowners, true
	}
	tags := GetCITagsReadOnly()
	locations := codeownership.Locations{
		Workspace:  tags[constants.CIWorkspacePath],
		Repository: tags[constants.GitRepositoryURL],
		Provider:   tags[constants.CIProviderName],
	}
	var rules *codeownership.Resolver
	var err error
	var info os.FileInfo
	if locations.Workspace != "" {
		info, err = os.Stat(locations.Workspace)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	}
	switch {
	case err != nil:
		// An unreadable workspace is retried, like an unreadable file.
	case info != nil && info.IsDir():
		// Source paths are relative to the workspace, the repository root, so
		// its CODEOWNERS file is read without looking for an enclosing Git root.
		// Without one, there are no rules: ddto's Git worker and every test
		// process must reach the same decision, whatever their directories.
		rules, err = codeownership.DiscoverIn(locations.Workspace, locations)
	default:
		// Standalone test binaries may have no CI workspace, or one that does
		// not exist where they run: as a last resort, look in the current
		// directory, then in the binary's directory.
		workingDirectory, _ := os.Getwd()
		for _, directory := range []string{workingDirectory, filepath.Dir(os.Args[0])} {
			rules, err = codeownership.DiscoverIn(directory, locations)
			if err != nil || rules != nil {
				break
			}
		}
	}
	if err != nil {
		logger.Debug("civisibility: CODEOWNERS discovery failed: %s", err)
		return nil, false
	}
	codeowners = rules
	codeownersLookupDone = true
	if rules != nil {
		logger.Debug("civisibility: CODEOWNERS loaded file=%q dialect=%d diagnostics=%d", rules.File(), rules.Dialect(), rules.Diagnostics())
	}
	return codeowners, true
}

// ResetCodeOwnersForTesting clears process-local discovery; readers must stop.
func ResetCodeOwnersForTesting() {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()
	codeowners = nil
	codeownersLookupDone = false
}
