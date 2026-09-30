package platform

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
)

func copyEnvironment(env map[string]string) map[string]string {
	result := make(map[string]string, len(env))
	maps.Copy(result, env)
	return result
}

func (j *JavaScript) RunEnv(options framework.RuntimeOptions) (map[string]string, error) {
	env := j.baseEnv()
	if options.Framework == "vitest" {
		env = addNodeImport(env, ddTraceRegisterModule)
	}
	maps.Copy(env, options.Env)
	appendNodePreloads(env, options.PreloadFiles)
	return env, nil
}

func (j *JavaScript) DiscoveryEnv(_ context.Context, kind framework.DiscoveryKind, options framework.RuntimeOptions) (map[string]string, error) {
	if kind != framework.FileDiscovery {
		return nil, fmt.Errorf("JavaScript full test discovery is not supported")
	}
	env := copyEnvironment(options.Env)
	current, found := env[nodeOptionsEnvVar]
	if !found {
		current, found = os.LookupEnv(nodeOptionsEnvVar)
	}
	if found {
		env[nodeOptionsEnvVar] = nodeOptionsWithoutImport(nodeOptionsWithoutRequire(current, ddTraceCIInitModule), ddTraceRegisterModule)
	}
	appendNodePreloads(env, options.PreloadFiles)
	return env, nil
}

func appendNodePreloads(env map[string]string, files []string) {
	if len(files) == 0 {
		return
	}
	current, found := env[nodeOptionsEnvVar]
	if !found {
		current = os.Getenv(nodeOptionsEnvVar)
	}
	for _, file := range files {
		current = strings.TrimSpace(current + " --require " + strconv.Quote(file))
	}
	env[nodeOptionsEnvVar] = current
}

func (r *Ruby) RunEnv(options framework.RuntimeOptions) (map[string]string, error) {
	if len(options.PreloadFiles) != 0 {
		return nil, fmt.Errorf("Ruby framework preloads are not supported")
	}
	env := r.baseEnv()
	maps.Copy(env, options.Env)
	return env, nil
}

func (r *Ruby) DiscoveryEnv(ctx context.Context, kind framework.DiscoveryKind, options framework.RuntimeOptions) (map[string]string, error) {
	switch kind {
	case framework.FileDiscovery:
		return copyEnvironment(options.Env), nil
	case framework.FullDiscovery:
		if err := r.SanityCheck(ctx); err != nil {
			return nil, fmt.Errorf("full test discovery requires datadog-ci: %w", err)
		}
		return r.RunEnv(options)
	default:
		return nil, fmt.Errorf("unknown discovery kind: %d", kind)
	}
}

func (p *Python) RunEnv(options framework.RuntimeOptions) (map[string]string, error) {
	if len(options.PreloadFiles) != 0 {
		return nil, fmt.Errorf("Python framework preloads are not supported")
	}
	env := p.baseEnv()
	maps.Copy(env, options.Env)
	return env, nil
}

func (p *Python) DiscoveryEnv(ctx context.Context, kind framework.DiscoveryKind, options framework.RuntimeOptions) (map[string]string, error) {
	switch kind {
	case framework.FileDiscovery:
		return copyEnvironment(options.Env), nil
	case framework.FullDiscovery:
		if err := p.SanityCheck(ctx); err != nil {
			return nil, fmt.Errorf("full test discovery requires ddtrace: %w", err)
		}
		return p.RunEnv(options)
	default:
		return nil, fmt.Errorf("unknown discovery kind: %d", kind)
	}
}
