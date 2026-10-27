package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestExhaustedFileDescriptorsAskForAnUnchangedRetry(t *testing.T) {
	for name, errno := range map[string]syscall.Errno{"system": syscall.ENFILE, "process": syscall.EMFILE} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
			drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
			drive.workflowRun = func(_ context.Context, out io.Writer) error {
				require.NoError(t, finishValidation(out, root, completeValidationFixture("vitest")))
				// The copy error as --all wraps it for one configuration.
				copyErr := &fs.PathError{Op: "open", Path: filepath.Join(root, "node_modules/lodash/cond.js"), Err: errno}
				return fmt.Errorf("npm test: %w", copyErr)
			}
			var output bytes.Buffer
			require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			require.Equal(t, "blocked_environment", result.Status)
			require.Equal(t, fileDescriptorsExhaustedCode, result.Code)
			require.Equal(t, "run_command", result.NextAction)
			require.NotEmpty(t, result.ResumeCommand)
			require.Equal(t, result.ResumeCommand, result.NextCommand, "retry the same validation unchanged")
			require.Contains(t, result.Needs, "open file descriptors")
			require.Empty(t, result.FinalResponse)
		})
	}
}
