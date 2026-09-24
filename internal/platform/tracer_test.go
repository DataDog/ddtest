package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTracerDetectionDistinguishesAbsenceFromProbeFailure(t *testing.T) {
	for _, language := range []string{"javascript", "python", "ruby"} {
		t.Run(language, func(t *testing.T) {
			executor := &mockCommandExecutor{}
			detect := func() (string, error) {
				switch language {
				case "javascript":
					return DetectJavaScriptTracer(t.Context(), executor)
				case "python":
					return DetectPythonTracer(t.Context(), executor, "python", nil)
				default:
					return DetectRubyTracer(t.Context(), executor, nil)
				}
			}
			result, err := detect()
			require.NoError(t, err)
			require.Empty(t, result)
			executor.combinedOutputErr = errors.New("runtime unavailable")
			_, err = detect()
			require.ErrorContains(t, err, "runtime unavailable")
		})
	}
}

func TestPythonTracerDetectionUsesSelectedEnvironment(t *testing.T) {
	executor := &mockCommandExecutor{combinedOutput: []byte("3.0.0\n"), onCombinedOutput: func(name string, args []string, env map[string]string) {
		require.Equal(t, "uv", name)
		require.Equal(t, []string{"run", "python", "-c"}, args[:3])
		require.Contains(t, args[3], "PackageNotFoundError")
	}}
	version, err := DetectPythonTracer(context.Background(), executor, "uv", []string{"run", "python"})
	require.NoError(t, err)
	require.Equal(t, "3.0.0", version) // Presence is independent of minimum supported version.
}
