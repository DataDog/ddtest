package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/offlineagent"
	"github.com/spf13/cobra"
)

var recordCmd = newRecordCommand()

const offlineAgentShutdownTimeout = 10 * time.Second

func newRecordCommand() *cobra.Command {
	var listen, payloadsDir, planDir string
	command := &cobra.Command{
		Use:   "record --payloads-dir path",
		Short: "Stand in for the Datadog Agent and save Test Optimization payloads as files",
		Long: `Stand in for the Datadog Agent while tests run on a machine that can't reach Datadog.

Point the tracing library at the address ddtest listens on (for example
DD_TRACE_AGENT_URL=http://127.0.0.1:8126) and turn agentless mode off. The
library sends its Test Optimization payloads here as it would through the
Agent's EVP proxy, and ddtest writes them as JSON files under --payloads-dir,
in the layout of the libraries' Bazel payload-files mode: tests/, coverage/
and telemetry/. Upload the files afterwards from a machine that can reach
Datadog.

Configuration requests are answered from the plan's HTTP cache under
--plan-dir, or with every feature off when the plan has no cache. Requests to
upload git data are answered as if Datadog already had it.

ddtest stops on SIGINT or SIGTERM, after saving the payloads it is receiving.`,
		Args: cobra.NoArgs,
		// The agent needs no git checkout, so skip the root command's check.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", listen, err)
			}
			return serveOfflineAgent(commandContext(cmd), listener, offlineagent.Config{
				PayloadsDir:  payloadsDir,
				HTTPCacheDir: filepath.Join(planDir, "cache", "http"),
			})
		},
	}
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:8126", "Address to listen on")
	command.Flags().StringVar(&payloadsDir, "payloads-dir", "", "Directory that receives the payload files")
	command.Flags().StringVar(&planDir, "plan-dir", constants.PlanDirectory, "Plan directory whose HTTP cache answers configuration requests")
	_ = command.MarkFlagRequired("payloads-dir")
	return command
}

// serveOfflineAgent serves the agent on listener until ctx is done, then
// waits for the requests in progress, so every payload a library sent is
// saved.
func serveOfflineAgent(ctx context.Context, listener net.Listener, config offlineagent.Config) error {
	server := offlineagent.Serve(listener, config)
	slog.Info("Offline agent listening", "address", listener.Addr().String(), "payloads_dir", config.PayloadsDir)

	select {
	case <-server.Done():
		return fmt.Errorf("offline agent stopped: %w", server.Err())
	case <-ctx.Done():
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), offlineAgentShutdownTimeout)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		return err
	}
	slog.Info("Offline agent stopped")
	return nil
}
