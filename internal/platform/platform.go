package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

type Platform interface {
	Name() string
	Detect(repositoryRoot string) (bool, error)
	CreateTagsMap(ctx context.Context) (map[string]string, error)
	DetectFramework() (framework.Framework, error)
	SanityCheck(ctx context.Context) error
	DetectTracer(ctx context.Context, options TracerOptions) (string, error)
	InstallTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error)
	TestSkippingLevel() settings.TestSkippingLevel
}

func detectAnyFile(repositoryRoot string, filenames ...string) (bool, error) {
	for _, filename := range filenames {
		path := filepath.Join(repositoryRoot, filename)
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return true, nil
			}
			continue
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	return false, nil
}

func runtimeTagProbeError(message string, output []byte, err error) error {
	if diagnostic := strings.TrimSpace(string(output)); diagnostic != "" {
		return fmt.Errorf("%s: %s: %w", message, diagnostic, err)
	}
	return fmt.Errorf("%s: %w", message, err)
}

// DetectPlatform selects a platform without running commands or checking installed
// runtimes/tracers. Selection uses the current directory and settings.
func DetectPlatform() (Platform, error) {
	root := "."
	frameworkHint := settings.GetFramework()
	platformHint := settings.GetPlatform()
	platforms := []Platform{NewJavaScript(), NewPython(), NewRuby(settings.GetTestSkippingLevel())}
	if platformHint != "" {
		for _, p := range platforms {
			if p.Name() == platformHint {
				return p, nil
			}
		}
		return nil, fmt.Errorf("unsupported platform: %s", platformHint)
	}
	var candidates []Platform
	for _, p := range platforms {
		if frameworkHint != "" {
			// An explicit framework also identifies its platform, including projects
			// whose custom configuration is not understood by automatic inspection.
			if _, err := p.DetectFramework(); err == nil {
				candidates = append(candidates, p)
			}
			continue
		}
		found, err := p.Detect(root)
		if err != nil {
			return nil, err
		}
		if found {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		if frameworkHint != "" {
			return nil, fmt.Errorf("unsupported framework: %s", frameworkHint)
		}
		return nil, fmt.Errorf("could not detect a supported platform; inspect package.json, Python project configuration, or Gemfile, or specify --platform")
	}
	if len(candidates) > 1 {
		names := make([]string, len(candidates))
		for i, p := range candidates {
			names[i] = p.Name()
		}
		return nil, fmt.Errorf("found multiple platforms (%s); select one with --platform or --framework", strings.Join(names, ", "))
	}
	return candidates[0], nil
}

func selectFramework(platform, hint string, candidates []framework.Framework) (framework.Framework, error) {
	if hint != "" {
		for _, candidate := range candidates {
			if candidate.Name() == hint {
				return candidate, nil
			}
		}
		return nil, fmt.Errorf("framework '%s' is not supported by platform '%s'", hint, platform)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("could not detect a supported %s test framework; specify --framework", platform)
	}
	if len(candidates) > 1 {
		names := make([]string, len(candidates))
		for i, f := range candidates {
			names[i] = f.Name()
		}
		return nil, fmt.Errorf("found multiple test frameworks (%s); select one with --framework", strings.Join(names, ", "))
	}
	return candidates[0], nil
}

// TracerOptions selects the test runtime and, only when no project tracer exists,
// the version and session directory for installation. Empty Version means latest.
type TracerOptions struct {
	Directory string
	Version   string
	Command   string
	Args      []string
}

// TracerInstallation describes the tracer selected for a local run.
// Env contains installation-specific overrides; project environments are preserved.
type TracerInstallation struct {
	Path    string
	Project bool
	Env     map[string]string
}

type commandExecutor interface {
	ext.CommandExecutor
	Output(context.Context, string, []string, map[string]string) ([]byte, []byte, error)
}

func tracerProbe(ctx context.Context, executor commandExecutor, command string, args []string, env map[string]string) (string, error) {
	output, stderr, err := executor.Output(ctx, command, args, env)
	if err != nil {
		return "", runtimeTagProbeError("detect project tracer", stderr, err)
	}
	return strings.TrimSpace(string(output)), nil
}
