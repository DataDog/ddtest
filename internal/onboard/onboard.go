// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package onboard prints the smallest supported Test Optimization setup.
package onboard

import (
	"fmt"
	"io"
	"os"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/platform"
)

// Run detects the first supported onboarding path and prints its instructions.
func Run(output io.Writer) error {
	repositoryRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find repository root: %w", err)
	}
	detectedPlatform, err := platform.DetectPlatform()
	if err != nil {
		return err
	}
	runner, err := detectedPlatform.DetectFramework()
	if err != nil {
		return err
	}
	provider, err := environment.DetectRepositoryCIProvider(repositoryRoot)
	if err != nil {
		return fmt.Errorf("onboard: %w", err)
	}
	switch provider := provider.(type) {
	case *environment.GitHub:
		return onboardGitHub(output, provider, repositoryRoot, detectedPlatform.Name(), runner.Name())
	default:
		return fmt.Errorf("onboard does not support CI provider %s", provider.Name())
	}
}
