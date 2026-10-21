// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"io"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
)

func onboardGitHub(output io.Writer, provider *environment.GitHub, repositoryRoot, language, name string) error {
	workflows, configured, err := provider.FindTestWorkflows(repositoryRoot, language, name)
	if err != nil {
		return err
	}
	if len(workflows) == 0 {
		return fmt.Errorf("onboard could not find a GitHub Actions workflow that runs %s", name)
	}

	_, _ = fmt.Fprintf(output, "DDTest found %s, %s, and GitHub Actions.\n", language, name)
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "Test workflow(s):")
	for _, workflow := range workflows {
		_, _ = fmt.Fprintf(output, "  - %s\n", workflow)
	}

	if len(configured) == len(workflows) {
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Datadog Test Optimization already appears in every detected test workflow.")
		_, _ = fmt.Fprintf(output, "Run `ddtest testdrive --framework %s` to check the setup locally.\n", name)
		_, _ = fmt.Fprintln(output, "After it finishes, post every `Open report:` link to the user so they can open the local Test Optimization report.")
		return nil
	}

	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, strings.TrimSpace(instructions(language, name)))
	return nil
}
