package platform

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/internal/version"
)

const (
	requiredGemName       = "datadog-ci"
	requiredGemMinVersion = "1.31.0"
)

// SanityCheck checks the prerequisite shared by Ruby execution and full discovery.
func (r *Ruby) SanityCheck(ctx context.Context) error {
	gemVersion, err := r.detectTracerVersion(ctx)
	if err != nil {
		return err
	}
	requiredVersion, err := version.Parse(requiredGemMinVersion)
	if err != nil {
		return err
	}
	if gemVersion.Compare(requiredVersion) < 0 {
		return fmt.Errorf("datadog-ci gem version %s is lower than required >= %s", gemVersion.String(), requiredVersion.String())
	}
	return nil
}

// detectTracerVersion reads the tracer version from the project's bundle.
func (r *Ruby) detectTracerVersion(ctx context.Context) (version.Version, error) {
	// Inherit project loaders without adding the instrumentation preload.
	output, err := r.executor.CombinedOutput(ctx, "bundle", []string{"info", requiredGemName}, nil)
	if err != nil {
		return version.Version{}, fmt.Errorf("detect project tracer: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return parseBundlerInfoVersion(string(output), requiredGemName)
}

// bundlerInfoRegex matches bundler info output format: "  * gem-name (version [hash])"
// Captures: 1=gem-name, 2=version
var bundlerInfoRegex = regexp.MustCompile(`^\s*\*\s+(\S+)\s+\((\d+\.\d+\.\d+)`)

func parseBundlerInfoVersion(output, gemName string) (version.Version, error) {
	for line := range strings.SplitSeq(output, "\n") {
		matches := bundlerInfoRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		matchedGem := matches[1]
		if matchedGem != gemName {
			continue
		}

		versionString := matches[2]
		parsed, err := version.Parse(versionString)
		if err != nil {
			return version.Version{}, fmt.Errorf("failed to parse version from bundle info output: %w", err)
		}

		return parsed, nil
	}

	return version.Version{}, fmt.Errorf("unable to find datadog-ci gem version in bundle info output")
}
