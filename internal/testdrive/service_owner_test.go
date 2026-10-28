package testdrive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

type crashedService struct {
	Address string
	Storage []string
}

func TestDeclaredServiceStopsWhenCoordinatorIsKilled(t *testing.T) {
	if root := os.Getenv("DDTEST_SERVICE_CRASH_ROOT"); root != "" {
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			t.Setenv(key, os.Getenv("DDTEST_SERVICE_CRASH_TMP"))
		}
		lock, err := workspace.AcquireLock(root)
		require.NoError(t, err)
		defer func() { _ = lock.Close() }()
		drive := &Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{Timeout: 20 * time.Millisecond}}
		item := configurationResult{TestCommand: onboard.TestCommand{Services: []onboard.ServiceCommand{{Argv: []string{"npm", "start"}, Directory: ".", ReadinessURL: "http://127.0.0.1:0", TimeoutSeconds: 5, PortEnvironment: "APP_PORT"}}}}
		_, stop, err := drive.startSetupServices(t.Context(), io.Discard, &item)
		defer func() { _ = stop() }()
		require.NoError(t, err)
		storage, err := filepath.Glob(filepath.Join(os.TempDir(), "ddtest-service-*"))
		require.NoError(t, err)
		state := crashedService{Address: item.ServiceExecutions[0].ReadinessURL, Storage: storage}
		data, err := json.Marshal(state)
		require.NoError(t, err)
		require.NoError(t, writeValidation(filepath.Join(root, "ready.json"), data))
		time.Sleep(time.Minute) // Parent uses SIGKILL; no Go cleanup can run.
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix ownership and SIGKILL fixture")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm unavailable")
	}
	temporary := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, temporary)
	}
	root := workspace.CanonicalRoot(t.TempDir())
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"start":"node server.cjs","test":"vitest run"}}`)
	requireWriteFile(t, filepath.Join(root, "server.cjs"), `process.on('SIGTERM',()=>{}); require('http').createServer((q,s)=>s.end('ready')).listen(Number(process.env.APP_PORT),'127.0.0.1')`)
	canary, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = canary.Close() }()
	binary, err := os.Executable()
	require.NoError(t, err)
	command := exec.Command(binary, "-test.run=^TestDeclaredServiceStopsWhenCoordinatorIsKilled$")
	command.Env = append(os.Environ(), "DDTEST_SERVICE_CRASH_ROOT="+root, "DDTEST_SERVICE_CRASH_TMP="+temporary)
	var diagnostic bytes.Buffer
	command.Stdout = &diagnostic
	command.Stderr = &diagnostic
	require.NoError(t, command.Start())
	t.Cleanup(func() { _ = command.Process.Kill() })
	ready := filepath.Join(root, "ready.json")
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 10*time.Second, 20*time.Millisecond)
	data, err := os.ReadFile(ready)
	require.NoError(t, err)
	var state crashedService
	require.NoError(t, json.Unmarshal(data, &state))
	require.NotEmpty(t, state.Storage)
	address, err := url.Parse(state.Address)
	require.NoError(t, err)
	connection, err := net.DialTimeout("tcp", address.Host, time.Second)
	require.NoError(t, err)
	_ = connection.Close()
	// The service must outlive multiple individual 20ms test-command budgets.
	time.Sleep(100 * time.Millisecond)
	connection, err = net.DialTimeout("tcp", address.Host, time.Second)
	require.NoError(t, err)
	_ = connection.Close()
	require.NoError(t, command.Process.Kill())
	require.Error(t, command.Wait(), diagnostic.String())
	require.Eventually(t, func() bool {
		connection, err := net.DialTimeout("tcp", address.Host, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
		}
		return err != nil
	}, 5*time.Second, 50*time.Millisecond, "owned service must stop after its coordinator dies")
	connection, err = net.DialTimeout("tcp", canary.Addr().String(), time.Second)
	require.NoError(t, err)
	_ = connection.Close()
	// Startup reclaims only marked service/socket storage for this repository.
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}))
	for _, directory := range state.Storage {
		require.NoDirExists(t, directory, fmt.Sprint(output.String()))
	}
}
