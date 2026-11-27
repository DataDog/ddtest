// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"fmt"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
)

func describeCI(cmd *cobra.Command, provider, file, metadataFile string) (onboard.PythonCICommandCatalog, error) {
	if provider != "gitlab" {
		return onboard.PythonCICommandCatalog{}, fmt.Errorf("CI catalog discovery currently supports --ci-provider gitlab")
	}
	if selected := settings.GetPlatform(); selected != "" && selected != "python" {
		return onboard.PythonCICommandCatalog{}, fmt.Errorf("CI catalog discovery currently supports Python only")
	}
	if selected := settings.GetFramework(); selected != "" && selected != "pytest" {
		return onboard.PythonCICommandCatalog{}, fmt.Errorf("CI catalog discovery currently supports pytest only")
	}
	if settings.GetRuntimeTags() != "" {
		return onboard.PythonCICommandCatalog{}, fmt.Errorf("CI catalogs retain declared environments; do not supply host runtime tags")
	}
	checkout, err := describeCheckout()
	if err != nil {
		return onboard.PythonCICommandCatalog{}, err
	}
	catalog, err := onboard.DescribePythonGitLabCatalog(cmd.Context(), checkout.SourceRoot, file, metadataFile)
	catalog.RepositoryURL, catalog.SHA = checkout.RepositoryURL, checkout.CommitSha
	return catalog, err
}
