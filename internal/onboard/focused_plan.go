package onboard

import (
	"fmt"
	"github.com/kballard/go-shellquote"
	"path/filepath"
	"slices"
	"strings"
)

// cucumberFocusedPlan retains setup for the selected invocation. CI variants
// with different preparation are ambiguous; an agent must select their scope.
func cucumberFocusedPlan(root, command string) (TestCommand, error) {
	framework := "cucumber"
	remaining := packageScriptExpansionLimit
	resolution := resolveJestSequence(root, root, command, nil, &remaining, framework)
	entries := resolution.Tests
	if len(entries) != 1 || len(entries[0].UnvalidatedSetup) > 0 || filepath.Clean(entries[0].Directory) != "." {
		return TestCommand{}, focusedPlanError(framework, resolution.Reason, entries)
	}
	selected := entries[0]
	scope, err := DiscoverValidationScope(root, framework)
	if err != nil {
		return TestCommand{}, err
	}
	leaf, err := cucumberInvocation(root, command)
	if err != nil {
		return TestCommand{}, err
	}
	var matched *TestCommand
	for _, entry := range scope.Commands {
		candidateLeaf, err := cucumberInvocation(root, entry.Command)
		if err != nil || candidateLeaf != leaf || entry.Directory != selected.Directory {
			continue
		}
		candidate := entry
		if entry.Command != command {
			candidate.Prerequisites = append(slices.Clone(entry.Prerequisites), entry.LifecyclePrerequisites...)
		}
		if matched != nil && (!slices.EqualFunc(matched.Prerequisites, candidate.Prerequisites, EqualPreparation) || !slices.Equal(matched.UnvalidatedSetup, entry.UnvalidatedSetup)) {
			return TestCommand{}, fmt.Errorf("%s command has different CI preparation variants; use --all", framework)
		}
		matched = &candidate
	}
	if matched != nil {
		selected.Prerequisites = matched.Prerequisites
		selected.UnvalidatedSetup = matched.UnvalidatedSetup
	}
	// Keep the customer's entry point and its package lifecycle hooks.
	selected.Command = command
	return selected, nil
}

// Coverage output/threshold flags do not change which Cucumber invocation needs
// a build. Keep the selected wrapper during execution; match only its runner tail.
func cucumberInvocation(root, command string) (string, error) {
	probe, err := CucumberProbeCommand(root, command)
	if err != nil {
		return "", err
	}
	words, err := shellquote.Split(probe)
	if err != nil {
		return "", err
	}
	for i, word := range words {
		if filepath.Base(word) == "cucumber-js" {
			return shellquote.Join(words[i:]...), nil
		}
	}
	return "", fmt.Errorf("cucumber executable could not be resolved for %s", command)
}

// focusedPlanError names why the selected command did not resolve, so an agent
// reports the blocker instead of probing rewrites of the customer's scripts.
func focusedPlanError(framework, reason string, entries []TestCommand) error {
	switch {
	case reason != "":
	case len(entries) == 1 && len(entries[0].UnvalidatedSetup) > 0:
		reason = strings.Join(entries[0].UnvalidatedSetup, "; ")
	case len(entries) == 1:
		reason = "it runs from " + entries[0].Directory + ", not the repository root"
	case len(entries) > 1:
		reason = fmt.Sprintf("it resolves to %d %s invocations", len(entries), framework)
	default:
		reason = "no " + framework + " invocation was found"
	}
	return fmt.Errorf("%s validation needs one resolved root invocation and its setup: %s. Use --all or select an explicit command; report this blocker rather than rewriting package scripts or CI to satisfy ddtest", framework, reason)
}
