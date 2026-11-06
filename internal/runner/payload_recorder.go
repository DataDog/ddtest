package runner

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/errcode"
	"github.com/DataDog/ddtest/internal/offlineagent"
)

const payloadRecorderStopTimeout = 30 * time.Second

// payloadRecorder lets the workers of one run save their Test Optimization
// payloads as files: it serves the offline agent on a free local port and
// points the workers' tracing libraries at it.
type payloadRecorder struct {
	server *offlineagent.Server
}

func startPayloadRecorder(payloadsDir string) (*payloadRecorder, error) {
	// Fail before running any test, rather than lose every payload.
	if err := os.MkdirAll(payloadsDir, 0o755); err != nil {
		return nil, errcode.WithCode(errcode.RunPayloadRecorderFailed, fmt.Errorf("create the payloads directory: %w", err))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errcode.WithCode(errcode.RunPayloadRecorderFailed, fmt.Errorf("listen for the workers' payloads: %w", err))
	}
	server := offlineagent.Serve(listener, offlineagent.Config{PayloadsDir: payloadsDir, HTTPCacheDir: constants.HTTPCacheDir})
	slog.Info("Recording Test Optimization payloads as files", "payloads_dir", payloadsDir, "agent_url", server.URL())
	return &payloadRecorder{server: server}, nil
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
	if err := r.server.Stop(stopCtx); err != nil {
		return errcode.WithCode(errcode.RunPayloadRecorderFailed, err)
	}
	slog.Info("Saved the Test Optimization payloads")
	return nil
}
