// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

type discoveryExecutor func([]string) ([]byte, error)

func (f discoveryExecutor) CombinedOutput(_ context.Context, _ string, args []string, _ map[string]string) ([]byte, error) {
	return f(args)
}

func TestProjectProbeKeepsLettersOnlyServerPattern(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "useForm.server.test.tsx")
	requireWriteFile(t, source, "original")
	path, err := createJestProbe(root, validationRun{Tests: []jestTest{{File: source, Status: "passed"}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Remove(path)) })
	require.Regexp(t, regexp.MustCompile(`^[a-zA-Z]+\.server\.test\.tsx$`), filepath.Base(path))
}

func TestProjectSelectionPreservesCommandScope(t *testing.T) {
	args := []string{"test", "--", "--config", "scripts/jest.js", "--selectProjects", "Web", "Server", "--ignoreProjects=Server", "--ci"}
	selected, ok := projectArgs("npm", args, "Web")
	require.True(t, ok)
	require.Equal(t, []string{"test", "--", "--config", "scripts/jest.js", "--ci", "--selectProjects", "Web"}, selected)
	_, ok = projectArgs("npm", args, "Server")
	require.False(t, ok)
	_, ok = projectArgs("npm", args, "Other")
	require.False(t, ok)
	require.Contains(t, args, "--ignoreProjects=Server", "must not mutate the full-suite command")
}

func TestEveryUnselectableProjectIsUnvalidated(t *testing.T) {
	for _, duplicateName := range []string{"", "same"} {
		run := preparedTestdrive(t)
		project := jestProject{}
		project.DisplayName.Name = duplicateName
		result := validationResult{Preflight: &jestPreflight{Projects: []jestProject{project, project}}}
		require.NoError(t, run.runJestFeatures(t.Context(), &bytes.Buffer{}, nil, "", &result))
		require.Len(t, result.Features, 12)
		for i, feature := range result.Features {
			require.Equal(t, "inconclusive", feature.Status)
			require.Equal(t, []string{"project 1", "project 2"}[i/6], feature.Project)
		}
	}
}

func TestUndiscoveredProbeCannotValidateWrongProject(t *testing.T) {
	run := preparedTestdrive(t)
	source := filepath.Join(run.repositoryRoot, "onlyExactName.test.js")
	requireWriteFile(t, source, "original")
	run.executor = discoveryExecutor(func(args []string) ([]byte, error) {
		if slices.Contains(args, "--runTestsByPath") {
			return []byte("[]"), nil
		}
		return json.Marshal([]string{source})
	})
	result := validationResult{Preflight: &jestPreflight{Projects: []jestProject{{Root: run.repositoryRoot}}}}
	require.NoError(t, run.runJestFeatures(t.Context(), &bytes.Buffer{}, nil, "", &result))
	require.Len(t, result.Features, 6)
	for _, feature := range result.Features {
		require.Equal(t, "inconclusive", feature.Status)
		require.Contains(t, feature.Reason, "not discovered")
	}
	require.Len(t, result.ProjectChecks, 1)
	require.False(t, result.ProjectChecks[0].Discovered)
	require.Contains(t, result.ProjectChecks[0].ProbeDiscoveryCommand, "--runTestsByPath")
	files, err := filepath.Glob(filepath.Join(run.repositoryRoot, "ddtest*"))
	require.NoError(t, err)
	require.Empty(t, files)
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}
