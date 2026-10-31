// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"fmt"
	"os"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/spf13/cobra"
)

// locatePackage places onboard and testdrive for a JavaScript package below
// the repository root that holds its CI configuration (see
// onboard.LocatePackage). Onboarding, CI-wide validation and saved reports
// run from the repository root with the package selected, wherever they are
// started. A focused testdrive run still executes in the package directory
// it is started in, and keeps its reports at the root as well.
func locatePackage(cmd *cobra.Command) error {
	platform.UsePackageDirectory("")
	platform.UseDetectionHint("")
	testdrive.UseReportRoot("", "")
	directory, err := os.Getwd()
	if err != nil {
		return nil
	}
	location, ok := onboard.LocatePackage(directory)
	switch {
	case !ok:
		return nil
	case location.Package == "":
		platform.UseDetectionHint(location.Hint)
		return nil
	case location.Root == onboard.CanonicalDirectory(directory):
		// At the root, the CI configuration names the package.
		platform.UsePackageDirectory(location.Package)
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "ddtest: the repository root has no project manifest; using the JavaScript package in %s/, which its CI configuration references.\n", location.Package)
		return nil
	case cmd.Name() == "testdrive" && focusedTestdrive(cmd):
		testdrive.UseReportRoot(directory, location.Root)
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "ddtest: the CI configuration is at the repository root %s; this run executes in %s/ and keeps its reports in %s/.testoptimization.\n", location.Root, location.Package, location.Root)
		return nil
	}
	if err := os.Chdir(location.Root); err != nil {
		return fmt.Errorf("change to the repository root %s: %w", location.Root, err)
	}
	platform.UsePackageDirectory(location.Package)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "ddtest: the CI configuration is at the repository root %s; onboarding runs there for the JavaScript package in %s/, with reports in %s/.testoptimization.\n", location.Root, location.Package, location.Root)
	return nil
}

// focusedTestdrive reports a testdrive run that validates one command or
// configuration in the current package rather than the discovered CI scope
// or a saved report.
func focusedTestdrive(cmd *cobra.Command) bool {
	all, _ := cmd.Flags().GetBool("all")
	report, _ := cmd.Flags().GetBool("report")
	return !all && !report
}
