// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/platform"
)

// PluginApplyOptions are onboard --apply's choices for a language whose CI
// setup ddtest writes only in part: the CI provider's plugin setup (see
// CIProviderWritesPluginSetup).
type PluginApplyOptions struct {
	// Preview shows the edit and writes nothing; otherwise it is written.
	Preview, AllowDirty bool
	// Text renders for a person instead of a coding agent.
	Text bool
	// Display names a framework in output.
	Display func(framework string) string
}

// ApplyPluginSetup is onboard --apply for a language whose only setup
// ddtest writes is the CI provider's plugin setup: it previews the
// configuration edit, or writes it, then hands off as onboarding does for
// the rest of the language's setup and validation. Whatever stops
// onboarding before any setup (an unsupported provider, a CI runtime to
// state) or a refused edit is reported by that handoff instead.
func ApplyPluginSetup(output io.Writer, options StartOptions, apply PluginApplyOptions) error {
	if apply.Text {
		options.Human = true
	}
	start := func() error { return StartWithOptions(output, options) }
	root, err := os.Getwd()
	if err != nil {
		return start()
	}
	detected, err := platform.DetectPlatform()
	if err != nil {
		return start()
	}
	framework, err := onboardingFramework(detected)
	if err != nil || CheckCIProviderSupport(root, detected.Name(), framework) != nil || RequiredCIRuntimes(context.Background(), root, detected.Name(), framework) != nil {
		return start()
	}
	site := options.Site
	if site == "" {
		site = os.Getenv("DD_SITE")
	}
	plan, err := PlanProviderSetup(root, framework, site)
	if err != nil {
		if apply.Text {
			return err
		}
		// The handoff gives the instructions, naming the refusal.
		return start()
	}
	if plan.Configured() {
		if apply.Text {
			if _, err := fmt.Fprintf(output, "%s CI setup is already configured; no edits.\n", apply.Display(framework)); err != nil || apply.Preview {
				return err
			}
		}
		return start()
	}
	diff, err := plan.Diff()
	if err != nil {
		return err
	}
	if apply.Preview {
		if _, err := io.WriteString(output, diff); err != nil {
			return err
		}
		next := "pass --apply --yes to apply it."
		if dirty, err := plan.DirtyFiles(); err == nil && len(dirty) > 0 {
			next = "commit or stash " + strings.Join(dirty, ", ") + " and pass --apply --yes, or pass --apply --yes --allow-dirty to extend those uncommitted edits."
		}
		_, err = fmt.Fprintln(output, "Preview only; no source files changed and no tests executed. Review this diff, then "+next)
		return err
	}
	changes, err := plan.Apply(apply.AllowDirty)
	if err != nil {
		var dirty *DirtyWorkflowGap
		if !apply.Text && errors.As(err, &dirty) {
			return errors.Join(err, WriteAgentResult(output, AgentResult{
				Status: "needs_input", Phase: "onboarding", Code: "dirty_workflow", Reason: err.Error(), Location: strings.Join(dirty.Files, ", "), SetupDiff: diff, NextAction: "supply_input",
				RequiredInput: []string{"Review setup_diff against the existing edits of " + strings.Join(dirty.Files, ", ") + ". Commit or stash them, then rerun the same command; add --allow-dirty only when the user approves extending those uncommitted edits."},
			}))
		}
		return err
	}
	options.SetupApplied = true
	if apply.Text {
		if _, err := io.WriteString(output, diff); err != nil {
			return err
		}
		for _, change := range changes {
			if _, err := fmt.Fprintf(output, "Updated %s (before %s; after %s).\n", change.Path, change.Before, change.After); err != nil {
				return err
			}
		}
		return start()
	}
	var handoff bytes.Buffer
	startErr := StartWithOptions(&handoff, options)
	var result AgentResult
	if err := json.Unmarshal(handoff.Bytes(), &result); err != nil {
		_, writeErr := output.Write(handoff.Bytes())
		return errors.Join(startErr, writeErr)
	}
	names := make([]string, 0, len(changes))
	for _, change := range changes {
		names = append(names, change.Path)
	}
	result.Changes, result.SetupDiff = changes, diff
	result.UndoInstructions = "Source edits remain applied: " + strings.Join(names, ", ") + ". To undo only these edits from the repository root, preserving earlier changes:\ngit apply -R --whitespace=nowarn <<'DDTEST_SETUP_REVERT'\n" + diff + "DDTEST_SETUP_REVERT\n"
	return errors.Join(startErr, WriteAgentResult(output, result))
}
