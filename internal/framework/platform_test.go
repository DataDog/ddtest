package framework

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/stretchr/testify/require"
)

// testPlatform supplies prepared environments; runtime policy is tested in platform.
type testPlatform struct {
	env          map[string]string
	discoveryErr error
	runErr       error
}

func (p *testPlatform) Name() string                      { return "test" }
func (p *testPlatform) SanityCheck(context.Context) error { return nil }
func (p *testPlatform) RunEnv(options RuntimeOptions) (map[string]string, error) {
	env := make(map[string]string)
	maps.Copy(env, p.env)
	maps.Copy(env, options.Env)
	// Emulate a platform accepting the framework's startup-file request.
	for _, file := range options.PreloadFiles {
		env["NODE_OPTIONS"] = strings.TrimSpace(env["NODE_OPTIONS"] + " --require " + strconv.Quote(file))
	}
	return env, p.runErr
}
func (p *testPlatform) DiscoveryEnv(_ context.Context, _ DiscoveryKind, options RuntimeOptions) (map[string]string, error) {
	env, _ := p.RunEnv(options)
	return env, p.discoveryErr
}

const (
	nodeOptionsEnvVar   = "NODE_OPTIONS"
	ddTraceCIInitModule = "dd-trace/ci/init"
)

// platformBoundaryExecutor records whether an environment error leaked into a command.
type platformBoundaryExecutor struct{ runs, probes int }

func (e *platformBoundaryExecutor) Run(context.Context, string, []string, map[string]string) error {
	e.runs++
	return nil
}
func (e *platformBoundaryExecutor) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	e.probes++
	return nil, errors.New("not a Rails application")
}
func (e *platformBoundaryExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	out, err := e.CombinedOutput(ctx, name, args, env)
	return out, nil, err
}

func TestFrameworksPropagatePlatformErrors(t *testing.T) {
	constructors := []func(Platform, *platformBoundaryExecutor) Framework{
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewRSpec(p); f.executor = e; return f },
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewMinitest(p); f.executor = e; return f },
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewPytest(p); f.executor = e; return f },
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewJest(p); f.executor = e; return f },
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewMocha(p); f.executor = e; return f },
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewVitest(p); f.executor = e; return f },
		func(p Platform, e *platformBoundaryExecutor) Framework {
			f := NewPlaywright(p)
			f.executor = e
			return f
		},
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewCypress(p); f.executor = e; return f },
		func(p Platform, e *platformBoundaryExecutor) Framework { f := NewCucumber(p); f.executor = e; return f },
	}
	for _, newFramework := range constructors {
		failure := errors.New("platform could not prepare the environment")
		p := &testPlatform{discoveryErr: failure, runErr: failure}
		e := &platformBoundaryExecutor{}
		fw := newFramework(p, e)
		t.Run(fw.Name(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.Same(t, p, fw.Platform())
			files := discovery.TestFileSet{Pattern: "**/*"}
			var err error
			if fw.SupportsFullTestDiscovery() {
				_, err = fw.DiscoverTests(t.Context(), files)
			} else {
				_, err = fw.DiscoverTestFiles(t.Context(), files)
			}
			require.ErrorIs(t, err, failure)
			require.Zero(t, e.probes, "discovery must stop before executing commands")
			require.ErrorIs(t, fw.RunTests(t.Context(), []string{"example_test.rb"}, nil), failure)
			require.Zero(t, e.runs, "test execution must stop when environment preparation fails")
		})
	}
}
