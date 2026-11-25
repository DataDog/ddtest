// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadGitLabCommandJobs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "base.yml", `variables:
  IMAGE: runner:fixed
.base:
  image: $IMAGE
  before_script: [prepare]
  script: [run-tests]
`)
	writeFile(t, root, "generated.yml", `include: [{local: base.yml}]
sample:
  extends: .base
  stage: test
  variables: {VALUE: literal}
  after_script: [cleanup]
support:
  script: [build]
`)
	jobs, gaps, err := ReadGitLabCommandJobs(root, "generated.yml")
	require.NoError(t, err)
	require.Empty(t, gaps)
	require.Len(t, jobs, 2)
	require.Equal(t, "sample", jobs[0].Name)
	require.Equal(t, "runner:fixed", jobs[0].Image)
	require.Equal(t, []string{"prepare"}, jobs[0].BeforeScript)
	require.Equal(t, []string{"run-tests"}, jobs[0].Script)
	require.Equal(t, []string{"cleanup"}, jobs[0].AfterScript)
	require.Equal(t, "literal", jobs[0].Variables["VALUE"])
	require.Equal(t, "support", jobs[1].Name)
}
func TestReadGitLabCommandJobsRetainsGaps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "generated.yml", `include: [{remote: "https://example.com/ci.yml"}]
sample:
  extends: .missing
  script: [run-tests]
`)
	jobs, gaps, err := ReadGitLabCommandJobs(root, "generated.yml")
	require.NoError(t, err)
	require.NotEmpty(t, gaps)
	require.Len(t, jobs, 1)
	require.NotEmpty(t, jobs[0].Unresolved)
	_, _, err = ReadGitLabCommandJobs(root, "../outside.yml")
	require.Error(t, err)
}
func TestGitLabRunnerArguments(t *testing.T) {
	args, err := GitLabRunnerArguments([]string{`scripts/run-tests "${venv_args[@]}" -- --ddtrace`})
	require.NoError(t, err)
	require.Equal(t, []string{"--ddtrace"}, args)
	args, err = GitLabRunnerArguments([]string{`"scripts/run-tests" "${venv_args[@]}" "--" -k 'alpha and not beta'`})
	require.NoError(t, err)
	require.Equal(t, []string{"-k", "alpha and not beta"}, args)
	for _, script := range []string{"custom-runner", `scripts/run-tests -- "${ARGS}"`, "scripts/run-tests; scripts/run-tests"} {
		_, err := GitLabRunnerArguments([]string{script})
		require.Error(t, err)
	}
}

func TestReadGitLabCommandJobsRespectsVariableInheritance(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "generated.yml", "variables:\n  TEST_SUITE: feature\n  TEST_ENVIRONMENTS_1: fixed\n  KEEP: value\nnone:\n  inherit: {variables: false}\n  script: [build]\nselected:\n  inherit: {variables: [KEEP]}\n  variables: {LOCAL: local}\n  script: [build]\n")
	jobs, _, err := ReadGitLabCommandJobs(root, "generated.yml")
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	require.Empty(t, jobs[0].Variables)
	require.Equal(t, map[string]string{"KEEP": "value", "LOCAL": "local"}, jobs[1].Variables)
}
