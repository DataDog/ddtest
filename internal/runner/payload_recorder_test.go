package runner

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/errcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

// sendingFramework sends one test cycle payload to the agent in its
// environment while it runs tests, as a tracing library would.
type sendingFramework struct {
	*MockFramework
	t      *testing.T
	status int
}

func (f *sendingFramework) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "version")
	payload = msgp.AppendInt(payload, 1)
	agentURL, client := envMap["DD_TRACE_AGENT_URL"], http.DefaultClient
	if socket, ok := strings.CutPrefix(agentURL, "unix://"); ok {
		agentURL = "http://agent"
		client = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL+"/evp_proxy/v4/api/v2/citestcycle", bytes.NewReader(payload))
	require.NoError(f.t, err)
	request.Header.Set("X-Datadog-EVP-Subdomain", "citestcycle-intake")
	response, err := client.Do(request)
	require.NoError(f.t, err)
	f.status = response.StatusCode
	require.NoError(f.t, response.Body.Close())
	return f.MockFramework.RunTests(ctx, testFiles, envMap)
}

func prepareSequentialRun(t *testing.T) {
	t.Helper()
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")
	writeRunnerTestFile(t, constants.TestFilesOutputPath, "spec/a_spec.rb\n")
}

func TestRunRecordsTheWorkersPayloadsWhenAsked(t *testing.T) {
	payloadsDir := filepath.Join(t.TempDir(), "payloads")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RECORD_PAYLOADS", payloadsDir)
	withRunnerTestSettings(t)
	prepareSequentialRun(t)
	framework := &sendingFramework{MockFramework: &MockFramework{FrameworkName: "rspec"}, t: t}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}

	require.NoError(t, NewWithDependencies(platform, framework, &fakePlanner{}).Run(context.Background()))

	assert.Equal(t, http.StatusAccepted, framework.status)
	saved, err := os.ReadDir(filepath.Join(payloadsDir, "tests"))
	require.NoError(t, err)
	assert.Len(t, saved, 1)
	calls := framework.GetRunTestsCalls()
	require.Len(t, calls, 1)
	agentURL := calls[0].EnvMap["DD_TRACE_AGENT_URL"]
	assert.Regexp(t, `^http://127\.0\.0\.1:\d+$`, agentURL)
	assert.Equal(t, "false", calls[0].EnvMap["DD_CIVISIBILITY_AGENTLESS_ENABLED"])
	_, err = http.Get(agentURL + "/info")
	assert.Error(t, err, "the recorder still answers after the run")
}

func TestRunRecordsOverAUnixSocketWhenAsked(t *testing.T) {
	assertRunRecordsOverAUnixSocket(t, "unix", false)
}

func TestRunRecordsOverAUnixSocketWhenThePlatformsTracerReachesOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("auto stays on TCP on Windows")
	}
	assertRunRecordsOverAUnixSocket(t, "auto", true)
}

func TestRunRecordsOverTCPWhenAsked(t *testing.T) {
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RECORD_PAYLOADS", t.TempDir())
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RECORD_PAYLOADS_TRANSPORT", "tcp")
	withRunnerTestSettings(t)
	prepareSequentialRun(t)
	framework := &sendingFramework{MockFramework: &MockFramework{FrameworkName: "jest"}, t: t}
	platform := &MockPlatform{PlatformName: "javascript", Framework: framework, UnixSocket: true}

	require.NoError(t, NewWithDependencies(platform, framework, &fakePlanner{}).Run(context.Background()))

	assert.Equal(t, http.StatusAccepted, framework.status)
	calls := framework.GetRunTestsCalls()
	require.Len(t, calls, 1)
	assert.Regexp(t, `^http://127\.0\.0\.1:\d+$`, calls[0].EnvMap["DD_TRACE_AGENT_URL"])
}

func assertRunRecordsOverAUnixSocket(t *testing.T, transport string, platformReachesUnixSocket bool) {
	t.Helper()
	payloadsDir := filepath.Join(t.TempDir(), "payloads")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RECORD_PAYLOADS", payloadsDir)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RECORD_PAYLOADS_TRANSPORT", transport)
	withRunnerTestSettings(t)
	prepareSequentialRun(t)
	framework := &sendingFramework{MockFramework: &MockFramework{FrameworkName: "rspec"}, t: t}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework, UnixSocket: platformReachesUnixSocket}

	require.NoError(t, NewWithDependencies(platform, framework, &fakePlanner{}).Run(context.Background()))

	assert.Equal(t, http.StatusAccepted, framework.status)
	saved, err := os.ReadDir(filepath.Join(payloadsDir, "tests"))
	require.NoError(t, err)
	assert.Len(t, saved, 1)
	calls := framework.GetRunTestsCalls()
	require.Len(t, calls, 1)
	agentURL := calls[0].EnvMap["DD_TRACE_AGENT_URL"]
	assert.Regexp(t, `^unix:///.+/agent\.sock$`, agentURL)
	_, err = os.Stat(filepath.Dir(strings.TrimPrefix(agentURL, "unix://")))
	assert.ErrorIs(t, err, os.ErrNotExist, "the socket's directory outlives the run")
}

func TestRunOverridesAgentlessModeWhenRecording(t *testing.T) {
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RECORD_PAYLOADS", t.TempDir())
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true;DD_TRACE_AGENT_URL=http://agent:8126")
	withRunnerTestSettings(t)
	prepareSequentialRun(t)
	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}

	require.NoError(t, NewWithDependencies(platform, framework, &fakePlanner{}).Run(context.Background()))

	calls := framework.GetRunTestsCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "false", calls[0].EnvMap["DD_CIVISIBILITY_AGENTLESS_ENABLED"])
	assert.NotEqual(t, "http://agent:8126", calls[0].EnvMap["DD_TRACE_AGENT_URL"])
}

func TestRunLeavesTheWorkersAloneByDefault(t *testing.T) {
	withRunnerTestSettings(t)
	prepareSequentialRun(t)
	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}

	require.NoError(t, NewWithDependencies(platform, framework, &fakePlanner{}).Run(context.Background()))

	calls := framework.GetRunTestsCalls()
	require.Len(t, calls, 1)
	assert.NotContains(t, calls[0].EnvMap, "DD_TRACE_AGENT_URL")
	assert.NotContains(t, calls[0].EnvMap, "DD_CIVISIBILITY_AGENTLESS_ENABLED")
}

func TestRunFailsBeforeTestsWhenThePayloadsDirectoryCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RECORD_PAYLOADS", filepath.Join(blocker, "payloads"))
	withRunnerTestSettings(t)
	prepareSequentialRun(t)
	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}

	err := NewWithDependencies(platform, framework, &fakePlanner{}).Run(context.Background())

	require.Error(t, err)
	assert.Equal(t, errcode.RunPayloadRecorderFailed, errcode.CodeOf(err))
	assert.Zero(t, framework.GetRunTestsCallsCount(), "tests ran without a place to save their payloads")
}
