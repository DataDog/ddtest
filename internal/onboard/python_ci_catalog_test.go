// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestDescribePythonGitLabCatalog(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	writeFile(t, root, "pytest.ini", "[pytest]\ntestpaths=other\n")
	writeFile(t, root, "tests/feature/test_one.py", "raise RuntimeError('must not execute')\n")
	writeFile(t, root, "generated.yml", `.base:
  script:
    - scripts/run-tests "${venv_args[@]}" -- --ddtrace
feature:
  extends: .base
  variables:
    TEST_SUITE: feature-suite
    TEST_ENVIRONMENTS_1: "env-a env-b missing"
support:
  script: [build]
`)
	writeFile(t, root, "environments.json", `{"suites":[{"name":"feature-suite","venvs":[
 {"hash":"env-a","python_version":"3.12","packages":"dependency==1","command":"pytest {cmdargs} tests/feature/"},
 {"hash":"env-b","python_version":"3.13","packages":"dependency==2","command":"pytest {cmdargs} tests/feature/"}
 ]}]}`)
	catalog, err := DescribePythonGitLabCatalog(context.Background(), root, "generated.yml", "environments.json")
	require.NoError(t, err)
	require.Equal(t, "suite", catalog.TestLevel)
	require.Len(t, catalog.Jobs, 2)
	entries := catalog.Jobs[0].Commands
	require.Len(t, entries, 3)
	for _, entry := range entries[:2] {
		require.Empty(t, entry.Unresolved)
		require.Equal(t, []string{"pytest", "--ddtrace", "tests/feature/"}, entry.Argv)
		require.Equal(t, []string{"tests/feature/test_one.py"}, entry.CandidateTestFiles)
	}
	require.Equal(t, "feature/env-a", entries[0].ID)
	require.Equal(t, "3.13", entries[1].PythonVersion)
	require.NotEmpty(t, entries[2].Unresolved)
	require.Empty(t, entries[2].CandidateTestFiles)
	require.NotEmpty(t, catalog.Jobs[1].Unresolved)
	require.Equal(t, []string{"build"}, catalog.Jobs[1].Script)
	data, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(data), "os.platform"))
	writeFile(t, root, "environments.json", `{"suites":[{"name":"feature-suite","venvs":[{"hash":"env-a"},{"hash":"env-a"}]}]}`)
	_, err = DescribePythonGitLabCatalog(context.Background(), root, "generated.yml", "environments.json")
	require.ErrorContains(t, err, "duplicate environment")
}
