// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"slices"
)

type runDefaults struct {
	WorkingDirectory string `yaml:"working-directory"`
	Shell            string `yaml:"shell"`
}

type ciDefaults struct {
	Run runDefaults `yaml:"run"`
}

type ciWorkflow struct {
	On       any `yaml:"on"`
	Path     string
	Defaults ciDefaults            `yaml:"defaults"`
	Env      map[string]string     `yaml:"env"`
	Jobs     map[string]runtimeJob `yaml:"jobs"`
	// Files are the other repository files the provider read for this
	// workflow, such as included configuration.
	Files []string `yaml:"-"`
	// Review lists configuration the provider read as outside every test
	// path, reported without blocking.
	Review []string `yaml:"-"`
	// Caveats summarize that configuration, one line per group.
	Caveats []string `yaml:"-"`
}

// readWorkflows reads the selected provider's CI configuration.
func readWorkflows(root string) ([]ciWorkflow, error) {
	provider, err := currentCIProvider(root)
	if err != nil {
		return nil, err
	}
	return readProviderWorkflows(provider, root)
}

// WithCIProviderDefaults adds the variables the selected CI provider sets in
// every job, without overriding the configuration's own.
func WithCIProviderDefaults(env map[string]string) map[string]string {
	return mergeEnvironment(activeOrDefaultCIProvider().environment(), env)
}

// CIProviderConfigurationFiles lists the selected CI provider's configuration
// files in root without parsing them.
func CIProviderConfigurationFiles(root string) ([]string, error) {
	return activeOrDefaultCIProvider().configurationFiles(root)
}

// CIConfigurationFiles lists every repository file the selected CI
// provider's configuration is read from, included files too. A
// configuration that cannot be read lists its configuration files only.
func CIConfigurationFiles(root string) []string {
	provider := activeOrDefaultCIProvider()
	files, _ := provider.configurationFiles(root)
	workflows, err := provider.readWorkflows(root)
	if err != nil {
		return files
	}
	for _, workflow := range workflows {
		for _, file := range append([]string{workflow.Path}, workflow.Files...) {
			if !slices.Contains(files, file) {
				files = append(files, file)
			}
		}
	}
	return files
}

// testWorkflowsWithSetup lists the workflows whose jobs run framework's tests
// and those where an active setup selecting language precedes every test step.
func testWorkflowsWithSetup(root string, workflows []ciWorkflow, language, framework string) (found, configured []string) {
	token := setupActionLanguage(language)
	for _, workflow := range workflows {
		tests, complete := false, true
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			setup := false
			for i, step := range job.Steps {
				if current, ok := datadogSetup(step); ok {
					selected, err := setupSelectsLanguage(current, nil, token)
					setup = setup || err == nil && selected
				}
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				if resolution.Review || !resolution.Matched && (resolution.Reason == "" || !stepMayStartTests(root, workflow, job, step, framework)) {
					continue
				}
				tests = true
				// A complete setup the job writes by hand counts as its own.
				handWritten := !setup && resolution.Matched && resolution.Reason == "" && func() bool {
					current := handWrittenCISetup(root, workflow, job, i, nil, language, framework, "")
					return current != nil && current.complete()
				}()
				complete = complete && (setup || handWritten) && resolution.Reason == ""
			}
		}
		if tests {
			found = append(found, workflow.Path)
			if complete {
				configured = append(configured, workflow.Path)
			}
		}
	}
	return found, configured
}

// readProviderWorkflows reads a provider's configuration with the user's
// runtime statements recorded on its jobs.
func readProviderWorkflows(provider ciProvider, root string) ([]ciWorkflow, error) {
	workflows, err := provider.readWorkflows(root)
	if err == nil {
		fillStatedRuntimes(workflows)
		checkFallbackPrerequisites(provider, workflows)
		checkout := []string{ciProjectDirectory, "${{github.workspace}}"}
		if name := provider.checkoutVariable(); name != "" {
			checkout = append(checkout, "$"+name, "${"+name+"}")
		}
		for _, workflow := range workflows {
			for name, job := range workflow.Jobs {
				job.Checkout = append(slices.Clone(checkout), job.Checkout...)
				workflow.Jobs[name] = job
			}
		}
	}
	return workflows, err
}

// ciTestWorkflowFinder overrides instruction discovery where a provider needs
// literal steps instead of the resolved workflow inspection used by default.
type ciTestWorkflowFinder interface {
	findTestWorkflows(root, language, framework string) ([]string, []string, error)
}

// findTestWorkflows applies the shared test/setup classification after a provider
// parses its configuration. GitHub's manual instructions retain their literal
// inspection; full discovery and validation still resolve composite actions.
func findTestWorkflows(provider ciProvider, root, language, framework string) ([]string, []string, error) {
	if finder, ok := provider.(ciTestWorkflowFinder); ok {
		return finder.findTestWorkflows(root, language, framework)
	}
	workflows, err := provider.readWorkflows(root)
	if err != nil {
		return nil, nil, err
	}
	found, configured := testWorkflowsWithSetup(root, workflows, language, framework)
	return found, configured, nil
}
