// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

const ServiceFromCodeOwnersEnv = "DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS"
const ServiceFromCodeOwnersFormatEnv = "DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS_FORMAT"
const defaultCodeOwnersServiceFormat = "service-$(owner)"

type testPackageLocation struct{ source, workingDirectory string }

var testPackageSource atomic.Pointer[testPackageLocation]
var codeOwnersServiceOnce sync.Once
var codeOwnersService string

// RegisterTestPackageSource records a generated test-package caller before
// TestMain can change directories. The raw compiler path also survives -c;
// trimpath paths are resolved with the existing source-path metadata.
func RegisterTestPackageSource(source string) {
	if testPackageSource.Load() != nil {
		return
	}
	directory := ""
	if !filepath.IsAbs(source) {
		directory, _ = os.Getwd()
	}
	testPackageSource.CompareAndSwap(nil, &testPackageLocation{source, directory})
}

// ServiceFromCodeOwners resolves the opt-in service once per process. Explicit
// DD_SERVICE remains authoritative; the environment is never modified.
// Missing/unreadable CODEOWNERS or a package outside the workspace preserve the
// usual repository/go.test fallback. A valid file with no owner uses not-owned.
func ServiceFromCodeOwners() string {
	if env.Get("DD_SERVICE") != "" || !infra.BoolEnv(ServiceFromCodeOwnersEnv, false) {
		return ""
	}
	codeOwnersServiceOnce.Do(func() {
		tags := GetCITagsReadOnly()
		workspace := tags[constants.CIWorkspacePath]
		if workspace == "" {
			return
		}
		directory, ok := codeOwnersPackageDirectory(workspace, tags, testPackageSource.Load())
		if !ok {
			return
		}
		owners := GetCodeOwners()
		if owners == nil {
			return
		}
		owner := "not-owned"
		if entry, found := owners.MatchDirectory(path.Clean("/" + directory)); found && len(entry.Owners) != 0 {
			owner = strings.TrimPrefix(entry.Owners[0], "@")
			if slash := strings.LastIndexByte(owner, '/'); slash >= 0 {
				owner = owner[slash+1:]
			}
			if owner == "" {
				owner = "not-owned"
			}
		}
		format := env.Get(ServiceFromCodeOwnersFormatEnv)
		if format == "" {
			format = defaultCodeOwnersServiceFormat
		}
		codeOwnersService = strings.ReplaceAll(format, "$(owner)", owner)
		log.Debug("civisibility: service from CODEOWNERS package=%q owner=%q service=%q", directory, owner, codeOwnersService)
	})
	return codeOwnersService
}

func canonicalServiceDirectory(directory string) string {
	if resolved, err := filepath.EvalSymlinks(directory); err == nil {
		return resolved
	}
	return filepath.Clean(directory)
}

func codeOwnersPackageDirectory(workspace string, tags map[string]string, location *testPackageLocation) (string, bool) {
	workspace = canonicalServiceDirectory(workspace)
	if location != nil && filepath.IsAbs(location.source) {
		directory := canonicalServiceDirectory(filepath.Dir(location.source))
		relative, ok := relativePathInsideWorkspace(workspace, directory)
		return filepath.ToSlash(relative), ok
	}
	if location != nil {
		source := resolveSourceFilePath(location.source, tags, buildInfoMainModulePath())
		// A resolved compiler path identifies the package even when a -c
		// binary runs from another package's directory. Unknown import paths
		// retain the registered working directory as their fallback.
		if source.RelativePath != cleanLogicalSourcePath(location.source) {
			directory := path.Dir(source.RelativePath)
			if !filepath.IsAbs(directory) && directory != ".." && !strings.HasPrefix(directory, "../") {
				return directory, true
			}
		}
	}
	workingDirectory := ""
	if location != nil {
		workingDirectory = location.workingDirectory
	}
	if workingDirectory == "" {
		workingDirectory, _ = os.Getwd()
	}
	relative, ok := relativePathInsideWorkspace(workspace, canonicalServiceDirectory(workingDirectory))
	return filepath.ToSlash(relative), ok
}

// ResetServiceFromCodeOwnersForTesting requires all service readers to have
// stopped. It does not change the existing CODEOWNERS or CI tag caches.
func ResetServiceFromCodeOwnersForTesting() {
	testPackageSource.Store(nil)
	codeOwnersServiceOnce = sync.Once{}
	codeOwnersService = ""
}
