package runner

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/errcode"
	"github.com/DataDog/ddtest/internal/offlineagent"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
)

const payloadRecorderStopTimeout = 30 * time.Second

// payloadRecorder lets the workers of one run save their Test Optimization
// payloads as files: it serves the offline agent on a free local port, or on
// a Unix domain socket, and points the workers' tracing libraries at it.
type payloadRecorder struct {
	server    *offlineagent.Server
	socketDir string
}

// startPayloadRecorder serves the agent on a Unix domain socket, in a
// directory of its own that only this user can open, when unixSocket is set,
// and on a free local TCP port otherwise.
func startPayloadRecorder(payloadsDir string, unixSocket bool) (*payloadRecorder, error) {
	// Fail before running any test, rather than lose every payload.
	if err := os.MkdirAll(payloadsDir, 0o755); err != nil {
		return nil, errcode.WithCode(errcode.RunPayloadRecorderFailed, fmt.Errorf("create the payloads directory: %w", err))
	}
	recorder := &payloadRecorder{}
	network, address := "tcp", "127.0.0.1:0"
	if unixSocket {
		socketDir, err := os.MkdirTemp(socketParentDir(), "ddtest-agent-")
		if err != nil {
			return nil, errcode.WithCode(errcode.RunPayloadRecorderFailed, fmt.Errorf("create the agent's socket directory: %w", err))
		}
		recorder.socketDir = socketDir
		network, address = "unix", filepath.Join(socketDir, "agent.sock")
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		recorder.removeSocketDir()
		return nil, errcode.WithCode(errcode.RunPayloadRecorderFailed, fmt.Errorf("listen for the workers' payloads: %w", err))
	}
	recorder.server = offlineagent.Serve(listener, offlineagent.Config{PayloadsDir: payloadsDir, HTTPCacheDir: constants.HTTPCacheDir})
	slog.Info("Recording Test Optimization payloads as files", "payloads_dir", payloadsDir, "agent_url", recorder.server.URL())
	return recorder, nil
}

// pointWorkers sends the workers' payloads to the recorder instead of
// Datadog, whatever the environment says.
func (r *payloadRecorder) pointWorkers(workerEnv map[string]string) {
	workerEnv["DD_TRACE_AGENT_URL"] = r.server.URL()
	workerEnv[constants.TestOptimizationAgentlessEnabledEnvironmentVariable] = "false"
}

// stop waits for the payloads in progress, even when ctx was cancelled, so
// the workers' last payloads are saved.
func (r *payloadRecorder) stop(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), payloadRecorderStopTimeout)
	defer cancel()
	defer r.removeSocketDir()
	if err := r.server.Stop(stopCtx); err != nil {
		return errcode.WithCode(errcode.RunPayloadRecorderFailed, err)
	}
	slog.Info("Saved the Test Optimization payloads")
	return nil
}

// recordOnUnixSocket reports whether the recorder serves the workers' agent on
// a Unix domain socket: when the transport is unix, or auto and the platform's
// tracing library reaches an agent on one. auto stays on TCP on Windows, where
// the tracing libraries' support for Unix domain sockets hasn't been checked.
func recordOnUnixSocket(transport settings.PayloadsTransport, p platform.Platform) bool {
	switch transport {
	case settings.PayloadsTransportUnix:
		return true
	case settings.PayloadsTransportTCP:
		return false
	default:
		return runtime.GOOS != "windows" && p.ReachesAgentOnUnixSocket()
	}
}

// socketParentDir is where the socket's directory goes: /tmp when there is
// one, since a socket's path is limited to about 104 bytes on macOS and 108
// on Linux, and TMPDIR can take most of that.
func socketParentDir() string {
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

// removeSocketDir removes the Unix domain socket's directory, if any; closing
// the listener has already removed the socket.
func (r *payloadRecorder) removeSocketDir() {
	if r.socketDir != "" {
		_ = os.RemoveAll(r.socketDir)
	}
}
