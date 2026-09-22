package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

type Platform interface {
	Name() string
	Detect(repositoryRoot string) (bool, error)
	CreateTagsMap(ctx context.Context) (map[string]string, error)
	DetectFramework(root, hint string) (framework.Framework, error)
	SanityCheck(ctx context.Context) error
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

// PlatformDetector defines interface for detecting platforms - needed to allow mocking in tests
type PlatformDetector interface {
	DetectPlatform(root, frameworkHint string) (Platform, error)
}

type DatadogPlatformDetector struct{}

func runtimeTagProbeError(message string, output []byte, err error) error {
	if diagnostic := strings.TrimSpace(string(output)); diagnostic != "" {
		return fmt.Errorf("%s: %s: %w", message, diagnostic, err)
	}
	return fmt.Errorf("%s: %w", message, err)
}

func (d *DatadogPlatformDetector) DetectPlatform(root, frameworkHint string) (Platform, error) {
	return DetectPlatform(root, frameworkHint)
}

// DetectPlatform selects a platform without running commands or checking installed
// runtimes/tracers. Empty root and frameworkHint use the current directory and settings.
func DetectPlatform(root, frameworkHint string) (Platform, error) {
	if root == "" {
		root = "."
	}
	if frameworkHint == "" {
		frameworkHint = settings.GetFramework()
	}
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
			if _, err := p.DetectFramework(root, frameworkHint); err == nil {
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

func NewPlatformDetector() PlatformDetector {
	return &DatadogPlatformDetector{}
}
