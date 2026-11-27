package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/offlineagent"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfflineAgentServesUntilCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serveOfflineAgent(ctx, listener, offlineagent.Config{PayloadsDir: t.TempDir()})
	}()
	url := "http://" + listener.Addr().String() + "/info"

	response, err := http.Get(url)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusOK, response.StatusCode)

	cancel()
	require.NoError(t, <-served)
	_, err = http.Get(url)
	assert.Error(t, err, "the agent still answers after stopping")
}

func TestOfflineAgentServesOnAUnixSocket(t *testing.T) {
	// Not t.TempDir(): a socket's path is limited to about 104 bytes on macOS.
	dir, err := os.MkdirTemp("/tmp", "ddtest-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	listener, err := agentListener("unix://" + socket)
	require.NoError(t, err)
	assert.Equal(t, "unix", listener.Addr().Network())
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serveOfflineAgent(ctx, listener, offlineagent.Config{PayloadsDir: t.TempDir()})
	}()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}

	response, err := client.Get("http://agent/info")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusOK, response.StatusCode)

	cancel()
	require.NoError(t, <-served)
	_, err = os.Stat(socket)
	assert.ErrorIs(t, err, os.ErrNotExist, "the socket outlives the agent")
}

func TestOfflineAgentReportsAnAddressItCannotListenOn(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })
	command := newRecordCommand()
	command.SetArgs([]string{"--payloads-dir", t.TempDir(), "--listen", taken.Addr().String()})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})

	assert.ErrorContains(t, command.Execute(), "listen on "+taken.Addr().String())
}

func TestOfflineAgentNeedsAPayloadsDirectory(t *testing.T) {
	command := newRecordCommand()
	command.SetArgs(nil)
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})

	assert.ErrorContains(t, command.Execute(), `"payloads-dir" not set`)
}

// The agent runs on machines that may have no git checkout, so it must not
// inherit the root command's git check. A taken address makes it fail right
// after that check, without serving.
func TestOfflineAgentRunsWithoutGit(t *testing.T) {
	originalLookPathFunc := git.LookPathFunc
	git.LookPathFunc = func(string) (string, error) { return "", errors.New("missing git") }
	t.Cleanup(func() { git.LookPathFunc = originalLookPathFunc })
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })
	root := &cobra.Command{Use: "ddtest", PersistentPreRunE: rootCmd.PersistentPreRunE}
	root.AddCommand(newRecordCommand())
	root.SetArgs([]string{"record", "--payloads-dir", filepath.Join(t.TempDir(), "payloads"), "--listen", taken.Addr().String()})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	err = root.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listen on", "the git check stopped the command first")
}

func TestRecordCommandIsRegistered(t *testing.T) {
	found := false
	for _, command := range rootCmd.Commands() {
		found = found || command == recordCmd
	}
	assert.True(t, found, "record should be added to the root command")
}

func TestRunCommandRecordsPayloadsOnlyWhenAsked(t *testing.T) {
	flag := runCmd.PersistentFlags().Lookup("record-payloads")
	require.NotNil(t, flag, "ddtest run should have --record-payloads")
	assert.Empty(t, flag.DefValue, "recording must stay opt-in")
	t.Cleanup(func() {
		_ = flag.Value.Set(flag.DefValue)
		flag.Changed = false
		viper.Reset()
		settings.Init()
	})
	viper.Reset()
	require.NoError(t, bindPersistentFlags(runCmd, runPersistentFlagBindings))
	dir := t.TempDir()

	require.NoError(t, runCmd.PersistentFlags().Set("record-payloads", dir))
	settings.Init()

	assert.Equal(t, dir, settings.GetRecordPayloads())
}
