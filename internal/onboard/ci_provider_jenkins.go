// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// jenkinsProvider is the --ci-provider value of Jenkins.
const jenkinsProvider = "jenkins"

// jenkins reads Jenkins Pipeline Jenkinsfiles. Which file a Jenkins job
// runs (its Script Path) is configured in Jenkins, so every Jenkinsfile
// candidate is read. Each leaf stage (declarative steps, a parallel branch,
// a matrix cell, or a scripted stage) becomes one job of the shared model,
// and each top-level statement of a literal sh script one step; each sh
// starts a fresh shell. Its setup mechanism is the Datadog Jenkins plugin,
// enabled for the whole pipeline with datadog(testOptimization: …) (see
// ci_provider_jenkins_setup.go). Anything ddtest cannot read statically, such
// as shared library steps, computed scripts, Groovy functions or tests started
// in containers, becomes an unresolved step: it is reported, never dropped or
// certified.
type jenkins struct{}

func (jenkins) name() string { return jenkinsProvider }

func (jenkins) displayName() string { return "Jenkins" }

// Jenkins exports these to every step; tools detect a Jenkins build by them.
func (jenkins) environment() map[string]string {
	return map[string]string{"JENKINS_URL": "https://jenkins.invalid/", "BUILD_ID": "1"}
}

func (jenkins) sharedSyntax() bool { return false }

// Jenkinsfiles are edited by hand: onboarding gives every language
// instructions.
func (jenkins) editsSetup() bool { return false }

// jenkinsDirectories are where Jenkinsfiles conventionally live besides the
// repository root.
var jenkinsDirectories = []string{"jenkins", ".jenkins", "ci", ".ci", "jenkinsfiles", "pipelines"}

// jenkinsFileName reports a Jenkinsfile name: Jenkinsfile, Jenkinsfile.ci,
// ci.Jenkinsfile or ci.jenkinsfile.
func jenkinsFileName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "jenkinsfile" || strings.HasPrefix(lower, "jenkinsfile.") || strings.HasSuffix(lower, ".jenkinsfile")
}

// configurationFiles lists the Jenkinsfiles at the repository root and in
// the conventional directories, without reading them.
func (jenkins) configurationFiles(root string) ([]string, error) {
	var files []string
	for _, directory := range append([]string{""}, jenkinsDirectories...) {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(directory)))
		if os.IsNotExist(err) || err != nil && directory != "" {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("find Jenkins configuration: %w", err)
		}
		for _, entry := range entries {
			file := path.Join(directory, entry.Name())
			if entry.IsDir() || !jenkinsFileName(entry.Name()) {
				continue
			}
			if info, ok := repositoryFile(root, root, file); ok && !info.IsDir() {
				files = append(files, file)
			}
		}
	}
	slices.Sort(files)
	return files, nil
}

func (j jenkins) readWorkflows(root string) ([]ciWorkflow, error) {
	files, err := j.configurationFiles(root)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	var workflows []ciWorkflow
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		// A file that cannot be read fails the read, as for every provider:
		// selection then leaves Jenkins out, and a selected Jenkins reports it.
		workflow, err := readJenkinsfile(root, file, string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if len(files) > 1 {
			workflow.Caveats = append(workflow.Caveats, file+": Jenkins selects each job's Jenkinsfile (its Script Path) in the job configuration, which ddtest cannot read, so it read every Jenkinsfile candidate: "+strings.Join(files, ", ")+". Tell the user which ones their Jenkins jobs run.")
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

func (j jenkins) findTestWorkflows(root, language, framework string) ([]string, []string, error) {
	workflows, err := j.readWorkflows(root)
	if err != nil {
		return nil, nil, err
	}
	found, configured := testWorkflowsWithSetup(root, workflows, language, framework)
	return found, configured, nil
}

func (jenkins) setupStep(step runtimeStep) (ciSetup, bool) {
	if step.Setup == nil {
		return ciSetup{}, false
	}
	return *step.Setup, true
}

// planSetupEdits never edits a Jenkinsfile: the setup is applied from the
// instructions (see editsSetup).
func (jenkins) planSetupEdits(string, []ciWorkflow, []ciSetupTarget, ciSetupRequest) ([]setupEdit, []string, error) {
	return nil, nil, &SetupGap{"Jenkins", "onboard --apply does not edit Jenkinsfiles; add the Datadog Jenkins plugin setup from onboard --agent instructions, then validate with testdrive --all."}
}
