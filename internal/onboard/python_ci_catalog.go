// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/kballard/go-shellquote"
)

type PythonCICommandCatalog struct {
	SchemaVersion int           `json:"schema_version"`
	CIProvider    string        `json:"ci_provider"`
	Source        string        `json:"source"`
	TestLevel     string        `json:"test_level"`
	RepositoryURL string        `json:"repository_url"`
	SHA           string        `json:"sha"`
	Jobs          []PythonCIJob `json:"jobs"`
	Unresolved    []string      `json:"unresolved,omitempty"`
}
type PythonCIJob struct {
	CICommandJob
	Commands []PythonCICommand `json:"commands"`
}
type PythonCICommand struct {
	PythonCatalogCommand
	EnvironmentHash    string   `json:"environment_hash"`
	PythonVersion      string   `json:"python_version"`
	Packages           string   `json:"packages"`
	CandidateTestFiles []string `json:"candidate_test_files"`
	Unresolved         string   `json:"unresolved,omitempty"`
}
type ciEnvironment struct {
	Hash          string `json:"hash"`
	PythonVersion string `json:"python_version"`
	Packages      string `json:"packages"`
	Command       string `json:"command"`
}

type PythonCatalogCommand struct {
	ID        string   `json:"id"`
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd"`
	TestRoots []string `json:"test_roots"`
}

// DescribePythonGitLabCatalog joins declared jobs with exported command environments.
func DescribePythonGitLabCatalog(ctx context.Context, root, file, metadataFile string) (PythonCICommandCatalog, error) {
	result := PythonCICommandCatalog{SchemaVersion: 3, CIProvider: "gitlab", Source: file, TestLevel: "suite", Jobs: []PythonCIJob{}}
	jobs, gaps, err := ReadGitLabCommandJobs(root, file)
	if err != nil {
		return result, err
	}
	result.Unresolved = gaps
	data, err := os.ReadFile(metadataFile)
	if err != nil {
		return result, err
	}
	var metadata struct {
		Suites []struct {
			Name  string          `json:"name"`
			Venvs []ciEnvironment `json:"venvs"`
		} `json:"suites"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return result, fmt.Errorf("read command environments: %w", err)
	}
	environments := map[string]map[string]ciEnvironment{}
	for _, suite := range metadata.Suites {
		if _, exists := environments[suite.Name]; exists {
			return result, fmt.Errorf("duplicate environment suite %q", suite.Name)
		}
		environments[suite.Name] = map[string]ciEnvironment{}
		for _, env := range suite.Venvs {
			if env.Hash == "" {
				return result, fmt.Errorf("empty environment hash in %s", suite.Name)
			}
			if _, exists := environments[suite.Name][env.Hash]; exists {
				return result, fmt.Errorf("duplicate environment %s/%s", suite.Name, env.Hash)
			}
			environments[suite.Name][env.Hash] = env
		}
	}
	for _, job := range jobs {
		described := PythonCIJob{CICommandJob: job, Commands: []PythonCICommand{}}
		suite := job.Variables["TEST_SUITE"]
		var hashKeys []string
		for key := range job.Variables {
			if strings.HasPrefix(key, "TEST_ENVIRONMENTS_") {
				hashKeys = append(hashKeys, key)
			}
		}
		slices.Sort(hashKeys)
		if suite == "" || len(hashKeys) == 0 {
			described.Unresolved = append(described.Unresolved, "no environment-to-test-command mapping; declared scripts are retained")
			result.Jobs = append(result.Jobs, described)
			continue
		}
		forwarded, forwardErr := GitLabRunnerArguments(job.Script)
		seen := map[string]bool{}
		for _, key := range hashKeys {
			for _, hash := range strings.Fields(job.Variables[key]) {
				entry := PythonCICommand{PythonCatalogCommand: PythonCatalogCommand{ID: job.Name + "/" + hash, Cwd: "."}, EnvironmentHash: hash, CandidateTestFiles: []string{}}
				env, ok := environments[suite][hash]
				switch {
				case seen[hash]:
					entry.Unresolved = "duplicate environment reference " + hash
				case !ok:
					entry.Unresolved = "no command metadata for " + suite + "/" + hash
				case env.PythonVersion == "" || env.Command == "":
					entry.Unresolved = "incomplete command environment " + suite + "/" + hash
				case forwardErr != nil:
					entry.Unresolved = forwardErr.Error()
				default:
					entry.PythonVersion, entry.Packages = env.PythonVersion, env.Packages
					argv, err := shellquote.Split(strings.ReplaceAll(env.Command, "{cmdargs}", shellquote.Join(forwarded...)))
					if err == nil {
						argv, err = framework.PytestCommandArguments(argv, job.Variables["PYTEST_ADDOPTS"])
					}
					entry.Argv = argv
					if err == nil {
						var roots []string
						roots, err = ciPytestRoots(argv)
						if err == nil {
							entry.CandidateTestFiles, entry.TestRoots, err = framework.NewPytest(nil).CandidateFiles(ctx, roots)
						}
					}
					if err != nil {
						entry.Unresolved = err.Error()
					}
				}
				seen[hash] = true
				described.Commands = append(described.Commands, entry)
			}
		}
		result.Jobs = append(result.Jobs, described)
	}
	return result, nil
}

func ciPytestRoots(argv []string) ([]string, error) {
	start := 1
	if filepath.Base(argv[0]) != "pytest" {
		start = 3
	}
	roots := []string{}
	for i := start; i < len(argv); i++ {
		arg := argv[i]
		if slices.Contains([]string{"--ddtrace", "--no-ddtrace", "-q", "-v", "-vv", "-x"}, arg) {
			continue
		}
		if slices.Contains([]string{"-k", "-m", "--maxfail", "--import-mode"}, arg) {
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("missing value for %s", arg)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return nil, fmt.Errorf("unsupported catalog argument %s", arg)
		}
		roots = append(roots, arg)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("catalog discovery requires explicit test directories")
	}
	return roots, nil
}
