package platform

import (
	"fmt"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

// projectPlatform owns language-specific project inspection and framework selection.
type projectPlatform interface {
	Platform
	detectFramework(root, hint string) (framework.Framework, error)
}

// DetectTestProject shares the normal platform/framework selection rules without
// running prerequisite checks. Onboarding must also work before installing tracers.
func DetectTestProject(root, hint string) (string, framework.Framework, error) {
	if hint == "" {
		hint = settings.GetFramework()
	}
	p, err := inspectPlatform(root, settings.GetPlatform(), hint)
	if err != nil {
		return "", nil, err
	}
	fw, err := p.detectFramework(root, hint)
	if err != nil {
		return "", nil, err
	}
	return p.Name(), fw, nil
}

func inspectPlatform(root, platformHint, frameworkHint string) (projectPlatform, error) {
	platforms := []projectPlatform{NewJavaScript(), NewPython(), NewRuby(settings.GetTestSkippingLevel())}
	if platformHint != "" {
		for _, p := range platforms {
			if p.Name() == platformHint {
				return p, nil
			}
		}
		return nil, fmt.Errorf("unsupported platform: %s", platformHint)
	}
	var candidates []projectPlatform
	for _, p := range platforms {
		if frameworkHint != "" {
			// An explicit framework also identifies its platform, including projects
			// whose custom configuration is not understood by automatic inspection.
			if _, err := p.detectFramework(root, frameworkHint); err == nil {
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
