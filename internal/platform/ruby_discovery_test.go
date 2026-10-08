package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

type rubyPrerequisiteExecutor struct {
	t      *testing.T
	output string
	err    error
}

func (e *rubyPrerequisiteExecutor) CombinedOutput(_ context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	e.t.Helper()
	require.Equal(e.t, "bundle", name, "the test command must not start")
	require.Equal(e.t, []string{"info", "datadog-ci"}, args)
	require.Nil(e.t, env, "the probe must inherit the project environment without adding instrumentation")
	return []byte(e.output), e.err
}

func (e *rubyPrerequisiteExecutor) Run(context.Context, string, []string, map[string]string) error {
	e.t.Fatal("the test command must not start")
	return nil
}

func TestRubyFullDiscoveryRequiresCompatibleTracer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		err    error
		want   string
	}{
		{name: "missing", output: "Could not find gem 'datadog-ci'", err: errors.New("bundle failed"), want: "Could not find gem"},
		{name: "outdated", output: "  * datadog-ci (1.30.9)", want: "lower than required >= 1.31.0"},
		{name: "unknown version", output: "  * datadog-ci", want: "unable to find datadog-ci gem version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &rubyPrerequisiteExecutor{t: t, output: tc.output, err: tc.err}
			ruby := NewRuby(settings.TestSkippingLevelTest)
			ruby.executor = executor
			for _, fw := range []framework.Framework{
				framework.NewRSpec(ruby),
				framework.NewMinitest(ruby),
			} {
				t.Run(fw.Name(), func(t *testing.T) {
					executor.t = t
					_, err := fw.DiscoverTests(t.Context(), discovery.TestFileSet{Pattern: "**/*.rb"})
					require.ErrorContains(t, err, "full test discovery requires datadog-ci")
					require.ErrorContains(t, err, tc.want)
					if tc.err != nil {
						require.ErrorIs(t, err, tc.err)
					}
				})
			}
		})
	}
}

func (e *rubyPrerequisiteExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	output, err := e.CombinedOutput(ctx, name, args, env)
	return output, nil, err
}
