package runner

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
// and on a free local TCP port otherwise; with upstream, it forwards there
// every request that isn't Test Optimization's.
func startPayloadRecorder(payloadsDir string, unixSocket bool, upstream *url.URL) (*payloadRecorder, error) {
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
	recorder.server = offlineagent.Serve(listener, offlineagent.Config{PayloadsDir: payloadsDir, HTTPCacheDir: constants.HTTPCacheDir, Upstream: upstream})
	forwarding := "none"
	if upstream != nil {
		forwarding = upstream.Redacted()
	}
	slog.Info("Recording Test Optimization payloads as files", "payloads_dir", payloadsDir, "agent_url", recorder.server.URL(), "upstream", forwarding)
	return recorder, nil
}

// The variables a tracing library takes its agent from, in the order it
// reads them, and the agent's port without one.
const (
	agentURLEnv      = "DD_TRACE_AGENT_URL"
	agentHostEnv     = "DD_AGENT_HOST"
	agentPortEnv     = "DD_TRACE_AGENT_PORT"
	defaultAgentPort = "8126"
)

// payloadsUpstream is the agent the recorder forwards the workers' other
// requests to: setting's URL, nothing for settings.RecordPayloadsNoUpstream,
// and otherwise the agent the workers would have reached without the
// recorder, from their environment, --worker-env's over the process's:
// DD_TRACE_AGENT_URL, or DD_AGENT_HOST and DD_TRACE_AGENT_PORT, localhost
// and 8126 for the one left out; none when neither is set, as before the
// recorder forwarded anything. A URL the environment gives that no tracing
// library could take is said and not forwarded to.
func payloadsUpstream(setting string, workerEnv map[string]string) *url.URL {
	if setting == settings.RecordPayloadsNoUpstream {
		return nil
	}
	if setting != "" {
		// Checked when the settings were read.
		upstream, _ := offlineagent.ParseUpstream(setting)
		return upstream
	}
	lookup := func(name string) string {
		if value, ok := workerEnv[name]; ok {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(os.Getenv(name))
	}
	if value := lookup(agentURLEnv); value != "" {
		upstream, err := offlineagent.ParseUpstream(value)
		if err != nil {
			slog.Warn("The workers' agent isn't one the recorder can forward to; it forwards nothing", "error", err)
			return nil
		}
		return upstream
	}
	host, port := lookup(agentHostEnv), lookup(agentPortEnv)
	if host == "" && port == "" {
		return nil
	}
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = defaultAgentPort
	}
	upstream, err := offlineagent.ParseUpstream("http://" + net.JoinHostPort(host, port))
	if err != nil {
		slog.Warn("The workers' agent isn't one the recorder can forward to; it forwards nothing", "error", err)
		return nil
	}
	return upstream
}

// pointWorkers sends the workers' payloads to the recorder instead of
// Datadog, whatever the environment says: their DD_TRACE_AGENT_URL is the
// recorder's, which forwards what isn't Test Optimization's to the agent
// that variable named before, if any (payloadsUpstream).
func (r *payloadRecorder) pointWorkers(workerEnv map[string]string) {
	workerEnv[agentURLEnv] = r.server.URL()
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
