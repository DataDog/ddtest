// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.
package utils

import (
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
	workspace := tags[constants.CIWorkspacePath]
	sourceRoot := ""
	if location := testPackageSource.Load(); location != nil && filepath.IsAbs(location.source) {
		sourceRoot = filepath.Dir(location.source)
	}
	if workspace == "" {
		// Standalone test binaries may not have CI workspace metadata.
		workspace, _ = os.Getwd()
		if sourceRoot == "" {
			sourceRoot = filepath.Dir(os.Args[0])
		}
	} else if sourceRoot != "" {
		// A dependency's compiler path must not select another checkout's rules.
		if _, inside := relativePathInsideWorkspace(canonicalServiceDirectory(workspace), canonicalServiceDirectory(sourceRoot)); !inside {
			sourceRoot = ""
		}
	}
	rules, err := codeownership.Discover(codeownership.Locations{
		Workspace: workspace, SourceRoot: sourceRoot,
		Repository: tags[constants.GitRepositoryURL], Provider: tags[constants.CIProviderName],
	})
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
