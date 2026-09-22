package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/DataDog/ddtest/internal/errcode"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
	"github.com/stretchr/testify/require"
)

// Only selection and prerequisite checks are involved; no runtime or backend.
type selectionPlatform struct {
	platform.Platform
	framework                   framework.Framework
	frameworkErr, sanityErr     error
	frameworkCalls, sanityCalls int
	sanityContext               context.Context
}

func (p *selectionPlatform) Name() string { return "javascript" }
func (p *selectionPlatform) DetectFramework(string, string) (framework.Framework, error) {
	p.frameworkCalls++
	return p.framework, p.frameworkErr
}
func (p *selectionPlatform) SanityCheck(ctx context.Context) error {
	p.sanityCalls++
	p.sanityContext = ctx
	return p.sanityErr
}

func TestResolveTestEnvironment(t *testing.T) {
	original := detectPlatform
	t.Cleanup(func() { detectPlatform = original })
	p := &selectionPlatform{framework: framework.NewJest()}
	calls := 0
	detectPlatform = func(root, hint string) (platform.Platform, error) {
		calls++
		require.Equal(t, ".", root)
		require.Equal(t, settings.GetFramework(), hint)
		return p, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	gotPlatform, gotFramework, err := resolveTestEnvironment(ctx, errcode.PlanPlatformDetectionFailed, errcode.PlanFrameworkDetectionFailed)
	require.NoError(t, err)
	require.Same(t, p, gotPlatform)
	require.Same(t, p.framework, gotFramework)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, p.frameworkCalls)
	require.Equal(t, 1, p.sanityCalls)
	require.Equal(t, ctx, p.sanityContext)
}

func TestCommandsRejectSelectionErrorsBeforePlanningOrExecution(t *testing.T) {
	for _, command := range []string{"plan", "run"} {
		for _, stage := range []string{"platform", "framework", "prerequisites"} {
			t.Run(command+"/"+stage, func(t *testing.T) {
				original := detectPlatform
				t.Cleanup(func() { detectPlatform = original })
				failure := errors.New("selection failed")
				p := &selectionPlatform{framework: framework.NewJest()}
				detectPlatform = func(string, string) (platform.Platform, error) {
					if stage == "platform" {
						return nil, failure
					}
					return p, nil
				}
				if stage == "framework" {
					p.frameworkErr = failure
				}
				if stage == "prerequisites" {
					p.sanityErr = failure
				}
				var err error
				code := errcode.PlanPlatformDetectionFailed
				if command == "plan" {
					err = planCommand(t.Context(), telemetry.NoopClient())
					if stage == "framework" {
						code = errcode.PlanFrameworkDetectionFailed
					}
				} else {
					r, runErr := newRunner(t.Context(), telemetry.NoopClient())
					err = runErr
					require.Nil(t, r)
					code = errcode.RunPlatformDetectionFailed
					if stage == "framework" {
						code = errcode.RunFrameworkDetectionFailed
					}
				}
				require.ErrorIs(t, err, failure)
				require.Equal(t, code, errcode.CodeOf(err))
			})
		}
	}
}
