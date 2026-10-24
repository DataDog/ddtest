package onboard

import (
	"fmt"
	"github.com/kballard/go-shellquote"
	"path/filepath"
	"slices"
)

// cucumberFocusedPlan retains setup for the selected invocation. CI variants
// with different preparation are ambiguous; an agent must select their scope.
func cucumberFocusedPlan(root, command string) (TestCommand, error) {
	framework := "cucumber"
	entries := ValidationCommands(root, command, framework)
	if len(entries) != 1 || len(entries[0].UnvalidatedSetup) > 0 || filepath.Clean(entries[0].Directory) != "." {
		return TestCommand{}, fmt.Errorf("%s validation needs one resolved root invocation and its setup; use --all or select an explicit command", framework)
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
