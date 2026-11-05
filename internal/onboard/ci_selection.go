// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/kballard/go-shellquote"
)

// CIProviderChoice is a user decision: several CI providers contain tests and
// ddtest configures one at a time. No command is suggested until it is made.
type CIProviderChoice struct{ Providers []string }

func (e *CIProviderChoice) Error() string {
	return "Several CI providers contain test commands: " + strings.Join(e.Providers, ", ") + ". Ask the user which provider to install Test Optimization on, then rerun with --ci-provider <provider>."
}

// activeProvider is the provider selected for this command; empty selects one
// automatically.
var activeProvider atomic.Value

// UseCIProvider selects the CI provider onboarding and testdrive read for the
// rest of the command, normally the result of SelectCIProvider. Empty selects
// it automatically from the repository again.
func UseCIProvider(name string) { activeProvider.Store(name) }

// ActiveCIProvider names the provider selected with UseCIProvider, or empty.
func ActiveCIProvider() string {
	name, _ := activeProvider.Load().(string)
	return name
}

// CIProviderNames lists the supported --ci-provider values.
func CIProviderNames() []string {
	names := make([]string, 0, len(ciProviders))
	for _, provider := range ciProviders {
		names = append(names, provider.name())
	}
	return names
}

// UnknownCIProviderError rejects a --ci-provider value no provider registers.
func UnknownCIProviderError(name string) error {
	return fmt.Errorf("unsupported --ci-provider %q; supported CI providers: %s", name, strings.Join(CIProviderNames(), ", "))
}

func ciProviderNamed(name string) ciProvider {
	for _, provider := range ciProviders {
		if provider.name() == name {
			return provider
		}
	}
	return nil
}

// defaultCIProvider applies when a repository has no CI configuration: the
// first registered provider, as onboarding behaved before providers were
// selectable.
func defaultCIProvider() ciProvider { return ciProviders[0] }

// SelectCIProvider resolves the CI provider to configure. A requested provider
// is used as given. Otherwise the only provider with configuration files is
// used without parsing them; with several, the ones whose configuration runs
// tests or leaves steps unresolved are candidates, and more than one candidate
// returns a *CIProviderChoice. A configuration that cannot be parsed never
// fails selection: that provider is not a candidate, and its files stay
// visible as review notes of the selected provider's scope. Empty means no
// provider has configuration files.
func SelectCIProvider(root, requested string) (string, error) {
	if requested != "" {
		if ciProviderNamed(requested) == nil {
			return "", UnknownCIProviderError(requested)
		}
		return requested, nil
	}
	var configured []ciProvider
	for _, provider := range ciProviders {
		// A listing error is reported when that provider is read.
		if files, err := provider.configurationFiles(root); err != nil || len(files) > 0 {
			configured = append(configured, provider)
		}
	}
	switch len(configured) {
	case 0:
		return "", nil
	case 1:
		return configured[0].name(), nil
	}
	var candidates []string
	for _, provider := range configured {
		workflows, err := provider.readWorkflows(root)
		if err == nil && slices.ContainsFunc(workflows, func(workflow ciWorkflow) bool { return workflowHasTests(root, workflow) }) {
			candidates = append(candidates, provider.name())
		}
	}
	switch len(candidates) {
	case 0:
		return configured[0].name(), nil
	case 1:
		return candidates[0], nil
	}
	return "", &CIProviderChoice{Providers: candidates}
}

// currentCIProvider is the provider selected with UseCIProvider, otherwise
// the one SelectCIProvider resolves, otherwise the default.
func currentCIProvider(root string) (ciProvider, error) {
	name := ActiveCIProvider()
	if name == "" {
		var err error
		if name, err = SelectCIProvider(root, ""); err != nil {
			return nil, err
		}
	}
	if name == "" {
		return defaultCIProvider(), nil
	}
	if provider := ciProviderNamed(name); provider != nil {
		return provider, nil
	}
	return nil, UnknownCIProviderError(name)
}

// displayCIText writes the shared model's checkout expression, which every
// provider's values use, in the selected provider's own syntax, for output.
func displayCIText(text string) string {
	reference := activeOrDefaultCIProvider().checkoutReference()
	return strings.NewReplacer(ciProjectDirectory, reference, "${{github.workspace}}", reference).Replace(text)
}

// displayCIError is err with its message in the selected provider's syntax
// (see displayCIText); it still wraps err.
func displayCIError(err error) error {
	if err == nil || !strings.Contains(err.Error(), "github.workspace") || activeOrDefaultCIProvider().checkoutReference() == ciProjectDirectory {
		return err
	}
	return displayedCIError{err}
}

type displayedCIError struct{ err error }

func (e displayedCIError) Error() string { return displayCIText(e.err.Error()) }
func (e displayedCIError) Unwrap() error { return e.err }

// activeOrDefaultCIProvider is the selected provider without consulting the
// repository: the default until UseCIProvider selects another.
func activeOrDefaultCIProvider() ciProvider {
	if provider := ciProviderNamed(ActiveCIProvider()); provider != nil {
		return provider
	}
	return defaultCIProvider()
}

// ciProviderFollowUpFlag returns the --ci-provider argument human follow-up
// commands need: only when several providers have configuration in root, so
// a rerun would ask again.
func ciProviderFollowUpFlag(root string) string {
	return ciProviderFlag(root) + ciStatementsFollowUp()
}

// ciStatementsFollowUp repeats the user's CI statements in a human follow-up
// command, which reports are bound to.
func ciStatementsFollowUp() string {
	if arguments := CIStatementArguments(); len(arguments) > 0 {
		return " " + shellquote.Join(arguments...)
	}
	return ""
}

func ciProviderFlag(root string) string {
	selected := ActiveCIProvider()
	if selected == "" {
		return ""
	}
	configured := 0
	for _, provider := range ciProviders {
		if files, err := provider.configurationFiles(root); err != nil || len(files) > 0 {
			configured++
		}
	}
	if configured < 2 {
		return ""
	}
	return " --ci-provider " + selected
}

// CIProviderDisplayName names the selected CI provider in output.
func CIProviderDisplayName() string { return activeOrDefaultCIProvider().displayName() }

// workflowHasTests reports whether any active step of workflow may run tests
// of a registered language, or cannot be resolved well enough to tell.
func workflowHasTests(root string, workflow ciWorkflow) bool {
	for _, job := range workflow.Jobs {
		if disabledCondition(job.If) {
			continue
		}
		if _, _, err := jobMatrix(job); err != nil {
			return true // An unresolved matrix can run tests.
		}
		for _, step := range job.Steps {
			if disabledCondition(step.If) {
				continue
			}
			if step.Run == "" && step.ResolutionError != "" {
				return true
			}
			for _, language := range discoveryLanguages {
				for _, framework := range language.frameworks() {
					resolution := resolveTestStep(root, workflow, job, step, language.name(), framework)
					if resolution.Matched || (!resolution.Review && resolution.Reason != "" && stepMayStartTests(root, workflow, job, step, framework)) || namesFramework(step.Run, framework, true) {
						return true
					}
				}
			}
		}
	}
	return false
}

// disabledCondition reports whether condition can never pass, such as false or
// ${{ false }}; anything unresolved may still run.
func disabledCondition(condition string) bool {
	if strings.TrimSpace(condition) == "" {
		return false
	}
	active, err := possibleJobCondition(condition, nil)
	return err == nil && !active
}

// ProviderChoiceResult asks the user to choose the CI provider to configure.
func ProviderChoiceResult(choice *CIProviderChoice) AgentResult {
	return AgentResult{Status: "needs_input", Phase: "discovery", Code: "ci_provider_required", Reason: choice.Error(), RequiredInput: choice.Providers, NextAction: "ask_user", Instruction: "Ask the user which CI provider should receive Test Optimization. Do not choose one or install on every provider. After their answer, rerun the original command with --ci-provider <provider>."}
}
