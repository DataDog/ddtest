package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

type Platform interface {
	framework.PlatformEnvironment
	Detect(repositoryRoot string) (bool, error)
	CreateTagsMap(ctx context.Context) (map[string]string, error)
	DetectFramework() (framework.Framework, error)
	DetectTracer(ctx context.Context, options TracerOptions) (string, error)
	TracerInstallCommand(options TracerOptions) (string, []string, error)
	InstallTestdriveTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error)
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

// fallbackPlatform is detected automatically only in repositories no other
// platform claims, so supporting a platform later never changes detection for
// repositories whose users already rely on another one.
type fallbackPlatform interface {
	fallback() bool
}

func isFallback(p Platform) bool {
	fallback, ok := p.(fallbackPlatform)
	return ok && fallback.fallback()
}

// packageDirectory is the JavaScript package, relative to the working
// directory, that onboarding selected when the package is below the
// repository root that holds the CI configuration. Empty is the working
// directory itself.
var packageDirectory string

// detectionHint explains, when no platform is detected, where the projects
// below the working directory are and how to select one.
var detectionHint string

// UsePackageDirectory selects the JavaScript package below the working
// directory that platform and framework detection read; empty restores the
// working directory.
func UsePackageDirectory(directory string) { packageDirectory = directory }

// PackageDirectory is the directory platform and framework detection read for
// a JavaScript package: "." unless UsePackageDirectory selected another.
func PackageDirectory() string {
	if packageDirectory == "" {
		return "."
	}
	return filepath.FromSlash(packageDirectory)
}

// UseDetectionHint adds hint to the error returned when no platform is
// detected; empty removes it.
func UseDetectionHint(hint string) { detectionHint = hint }

// HasProjectManifest reports whether any platform detects a project in root.
func HasProjectManifest(root string) (bool, error) {
	for _, p := range []Platform{NewJavaScript(), NewPython(), NewRuby(settings.GetTestSkippingLevel()), NewJava()} {
		found, err := p.Detect(root)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// platformAliases names the platform a common alternative spelling means.
var platformAliases = map[string]string{"node": "javascript", "nodejs": "javascript", "js": "javascript", "typescript": "javascript", "py": "python", "rb": "ruby"}

// DetectPlatform selects a platform without running commands or checking installed
// runtimes/tracers. Selection uses the current directory and settings.
func DetectPlatform() (Platform, error) {
	root := "."
	frameworkHint := settings.GetFramework()
	platformHint := settings.GetPlatform()
	platforms := []Platform{NewJavaScript(), NewPython(), NewRuby(settings.GetTestSkippingLevel()), NewJava()}
	if platformHint != "" {
		names := make([]string, len(platforms))
		for i, p := range platforms {
			if p.Name() == platformHint {
				return p, nil
			}
			names[i] = p.Name()
		}
		if alias := platformAliases[strings.ToLower(platformHint)]; alias != "" {
			return nil, fmt.Errorf("unsupported platform: %s; use --platform %s (supported platforms: %s)", platformHint, alias, strings.Join(names, ", "))
		}
		return nil, fmt.Errorf("unsupported platform: %s; supported platforms: %s", platformHint, strings.Join(names, ", "))
	}
	if packageDirectory != "" {
		// Onboarding selected a JavaScript package below the repository root,
		// which may hold another platform's manifest of its own.
		javascript := platforms[0]
		if frameworkHint != "" {
			if _, err := javascript.DetectFramework(); err == nil {
				return javascript, nil
			}
		} else if found, err := javascript.Detect(PackageDirectory()); err != nil || found {
			return javascript, err
		}
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
		if detectionHint != "" {
			return nil, fmt.Errorf("could not detect a supported platform: %s", detectionHint)
		}
		return nil, fmt.Errorf("could not detect a supported platform; inspect package.json, Python project configuration, Gemfile, or pom.xml, or specify --platform")
	}
	if len(candidates) > 1 {
		// A fallback platform never competes with one that already claims the
		// repository; select it with --platform or --framework.
		if primary := slices.DeleteFunc(slices.Clone(candidates), isFallback); len(primary) > 0 {
			candidates = primary
		}
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

// TracerOptions selects the test runtime and the version and session directory
// for fallback installation when the project tracer check fails. Empty Version means latest.
type TracerOptions struct {
	Environment map[string]string
	Directory   string
	Version     string
	Command     string
	Args        []string
	// Testdrive's per-command limit for tracer installation; zero leaves it unbounded.
	Timeout time.Duration
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

// boundedExecutor applies a per-command limit to the default executor, so a
// stalled package manager or lifecycle script cannot block testdrive forever.
func boundedExecutor(executor commandExecutor, timeout time.Duration) commandExecutor {
	if base, ok := executor.(*ext.DefaultCommandExecutor); ok && timeout > 0 {
		bounded := *base
		bounded.Timeout = timeout
		return &bounded
	}
	return executor
}

func tracerProbe(ctx context.Context, executor commandExecutor, command string, args []string, env map[string]string) (string, error) {
	// Pass a private result file as the final argument. Runtime preloads and
	// shutdown hooks can log to either output stream before or after the probe.
	result, err := os.CreateTemp("", "ddtest-tracer-probe-*")
	if err != nil {
		return "", fmt.Errorf("create tracer probe result: %w", err)
	}
	defer func() { _ = os.Remove(result.Name()) }()
	if err := result.Close(); err != nil {
		return "", fmt.Errorf("close tracer probe result: %w", err)
	}
	probeArgs := append(append([]string{}, args...), result.Name())
	stdout, stderr, err := executor.Output(ctx, command, probeArgs, env)
	if err != nil {
		return "", runtimeTagProbeError("detect project tracer", append(stdout, stderr...), err)
	}
	output, err := os.ReadFile(result.Name())
	if err != nil {
		return "", fmt.Errorf("read tracer probe result: %w", err)
	}
	if len(output) == 0 {
		return "", fmt.Errorf("tracer probe returned no result")
	}
	return string(output), nil
}
