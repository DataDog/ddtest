package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func commandSetFixture(t *testing.T) string {
	t.Chdir(t.TempDir())
	viper.Reset()
	settings.Init()
	environment.ResetCITags()
	t.Cleanup(func() { viper.Reset(); settings.Init(); environment.ResetCITags() })
	for _, args := range [][]string{
		{"init", "-q"}, {"remote", "add", "origin", "https://example.com/org/repo.git"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "--allow-empty", "-qm", "initial"},
	} {
		output, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, string(output))
	}
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(sha))
}

func describedFixture(t *testing.T, id, service, sha string, files ...string) commandDescription {
	tags := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(describeRuntimeTags), &tags))
	return commandDescription{SchemaVersion: 2,
		Command:            describedCommand{ID: id, Argv: []string{"python", "-m", "pytest"}, Cwd: ".", TestRoots: []string{"tests"}},
		TIARequest:         describedRequest{Service: service, Env: "ci", RepositoryURL: "https://example.com/org/repo.git", SHA: sha, TestLevel: settings.TestSkippingLevelSuite, Configurations: tags},
		CandidateTestFiles: files}
}

func writeCommandSet(t *testing.T, jobs map[string][]commandDescription, order ...string) {
	set := map[string]any{"schema_version": 1}
	list := []any{}
	for _, name := range order {
		list = append(list, map[string]any{"name": name, "commands": jobs[name]})
	}
	set["jobs"] = list
	data, err := json.Marshal(set)
	require.NoError(t, err)
	describeTestFile(t, "commands.json", string(data))
}

func TestPlanCommandSetSkipsCommandsWithoutAffectedSuites(t *testing.T) {
	sha := commandSetFixture(t)
	describeTestFile(t, "tests/test_a.py", "raise AssertionError('must not collect')\n")
	describeTestFile(t, "tests/test_b.py", "raise AssertionError('must not collect')\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			_, _ = w.Write([]byte(`{"data":{"attributes":{"itr_enabled":true,"tests_skipping":true}}}`))
		case "/api/v2/ci/tests/skippable":
			_, _ = w.Write([]byte(`{"data":[{"type":"suite","attributes":{"suite":"test_a.py","configurations":{"test.bundle":"tests"}}}]}`))
		case "/api/v2/ci/ddtest/test_suite_durations":
			_, _ = w.Write([]byte(`{"data":{"attributes":{"test_suites":{"tests":{"test_a.py":{"source_file":"tests/test_a.py"},"test_b.py":{"source_file":"tests/test_b.py"}}}}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer server.Close()
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", server.URL)
	t.Setenv("DD_API_KEY", "fixture")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_SERVICE", "svc")
	t.Setenv("DD_ENV", "ci")
	writeCommandSet(t, map[string][]commandDescription{
		"only-a":    {describedFixture(t, "only-a", "svc", sha, "tests/test_a.py")},
		"both":      {describedFixture(t, "both", "svc", sha, "tests/test_a.py", "tests/test_b.py")},
		"elsewhere": {describedFixture(t, "elsewhere", "other-service", sha, "tests/test_a.py")},
	}, "only-a", "both", "elsewhere")

	command := catalogTestCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--commands", "commands.json", "--output", "selection.json"})
	require.NoError(t, command.Execute())
	var result catalogSelection
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Len(t, result.Commands, 3)
	require.Equal(t, "skip", result.Commands[0].Decision)
	require.Equal(t, "run", result.Commands[1].Decision)
	require.Equal(t, []string{"tests/test_b.py"}, result.Commands[1].RunnableFiles)
	require.Equal(t, "run", result.Commands[2].Decision)
	require.Contains(t, result.Commands[2].Reason, "worker identity override")
	require.Equal(t, []string{"skip", "run", "run"}, []string{result.Jobs[0].Decision, result.Jobs[1].Decision, result.Jobs[2].Decision})
	require.Equal(t, sha, result.SHA)
}

func TestPlanCommandSetRejectsStaleOrMixedInput(t *testing.T) {
	sha := commandSetFixture(t)
	writeCommandSet(t, map[string][]commandDescription{"job": {describedFixture(t, "job", "svc", strings.Repeat("0", 40), "tests/test_a.py")}}, "job")
	command := catalogTestCommand()
	command.SetArgs([]string{"--commands", "commands.json", "--output", "selection.json"})
	require.ErrorContains(t, command.Execute(), "was described at")

	writeCommandSet(t, map[string][]commandDescription{"job": {describedFixture(t, "job", "svc", sha)}}, "job")
	for _, args := range [][]string{
		{"--commands", "commands.json"},
		{"--commands", "commands.json", "--ci-provider", "gitlab", "--output", "selection.json"},
		{"--commands", "commands.json", "--output", "selection.json", "tests"},
	} {
		command := catalogTestCommand()
		command.SetArgs(args)
		require.Error(t, command.Execute(), args)
	}
	describeTestFile(t, "commands.json", `{"schema_version":1,"jobs":[{"name":"a","commands":[]},{"name":"a","commands":[]}]}`)
	command = catalogTestCommand()
	command.SetArgs([]string{"--commands", "commands.json", "--output", "selection.json"})
	require.ErrorContains(t, command.Execute(), "unique")
}

func TestFilterTestsLocation(t *testing.T) {
	files := []string{"tests/test_apps.py", "tests/test_views.py", "tests/sub/test_x.py"}
	kept, err := filterTestsLocation(files, "tests/test_[a-h]*.py")
	require.NoError(t, err)
	require.Equal(t, []string{"tests/test_apps.py"}, kept)
	kept, err = filterTestsLocation(files, "")
	require.NoError(t, err)
	require.Equal(t, files, kept)
	_, err = filterTestsLocation(files, "nothing/*.py")
	require.Error(t, err)
	_, err = filterTestsLocation(files, "tests/[")
	require.Error(t, err)
}

func TestCommandSetRejectsAnotherRepositoryAtSameCommit(t *testing.T) {
	sha := commandSetFixture(t)
	description := describedFixture(t, "unit", "svc", sha, "tests/test_case.py")
	description.TIARequest.RepositoryURL = "https://example.com/other/fork.git"
	writeCommandSet(t, map[string][]commandDescription{"unit": {description}}, "unit")
	_, err := loadCommandSet("commands.json")
	require.ErrorContains(t, err, "another repository")
}

func TestCommandRepositoryIdentity(t *testing.T) {
	for _, remote := range []string{"https://example.com/org/repo.git", "git@example.com:org/repo.git", "ssh://git@example.com/org/repo", "https://user:secret@example.com/org/repo/"} {
		require.True(t, sameCommandRepository(remote, "https://example.com/org/repo.git"), remote)
	}
	require.True(t, sameCommandRepository("git@github.com:Example/Repo.git", "https://github.com/example/repo"))
	for _, remote := range []string{"", "https://elsewhere.com/org/repo", "https://example.com/other/repo", "https://example.com/Org/repo", "https://example.com:8443/org/repo", "https://%invalid"} {
		require.False(t, sameCommandRepository(remote, "https://example.com/org/repo.git"), remote)
	}
}
