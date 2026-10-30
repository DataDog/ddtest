// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jenkinsApplyRepository is a repository named app for framework (jest,
// vitest or rspec) whose only CI is jenkinsfile, with Jenkins selected.
func jenkinsApplyRepository(t *testing.T, framework, jenkinsfile string) string {
	t.Helper()
	var root string
	switch framework {
	case "rspec":
		root = copyFixture(t, "jenkins-ruby")
		writeFile(t, root, "app.gemspec", "Gem::Specification.new do |spec|\n  spec.name = \"app\"\n  spec.version = \"1.0.0\"\n  spec.files = Dir[\"lib/**/*.rb\"]\nend\n")
	case "vitest":
		root = t.TempDir()
		writeFile(t, root, "package.json", `{"name": "app", "scripts": {"test": "vitest run"}, "devDependencies": {"vitest": "3.2.4"}}`)
	default:
		root = t.TempDir()
		writeFile(t, root, "package.json", `{"name": "app", "scripts": {"test": "jest"}, "devDependencies": {"jest": "30.0.0"}}`)
	}
	writeFile(t, root, "Jenkinsfile", jenkinsfile)
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	return root
}

// jenkinsApplyFramework is the framework a golden case runs, from its name.
func jenkinsApplyFramework(name string) string {
	for _, framework := range []string{"rspec", "vitest"} {
		if strings.HasPrefix(name, framework+"-") {
			return framework
		}
	}
	return "jest"
}

// Every case in testdata/jenkins-apply is a Jenkinsfile before onboard
// --apply, and either the exact file after it or the refusal it returns,
// which writes nothing. Applying a golden result again changes nothing.
func TestJenkinsApplyGolden(t *testing.T) {
	cases, err := os.ReadDir(filepath.Join("testdata", "jenkins-apply"))
	require.NoError(t, err)
	require.NotEmpty(t, cases)
	for _, entry := range cases {
		t.Run(entry.Name(), func(t *testing.T) {
			directory, err := filepath.Abs(filepath.Join("testdata", "jenkins-apply", entry.Name()))
			require.NoError(t, err)
			before, err := os.ReadFile(filepath.Join(directory, "before.groovy"))
			require.NoError(t, err)
			framework := jenkinsApplyFramework(entry.Name())
			root := jenkinsApplyRepository(t, framework, string(before))
			plan, err := PlanProviderSetup(root, framework, "datadoghq.com")
			if refusal, readErr := os.ReadFile(filepath.Join(directory, "refusal.txt")); readErr == nil {
				var gap *SetupGap
				require.ErrorAs(t, err, &gap)
				require.Contains(t, err.Error(), strings.TrimSpace(string(refusal)))
				require.Regexp(t, `onboard --instructions|rerun onboard --apply|host agent`, err.Error(), "a refusal says what to do instead")
				return
			}
			require.NoError(t, err)
			want, err := os.ReadFile(filepath.Join(directory, "after.groovy"))
			require.NoError(t, err)
			require.Len(t, plan.edits, 1)
			require.Equal(t, "Jenkinsfile", plan.edits[0].path)
			require.Equal(t, string(want), string(plan.edits[0].after))

			_, err = plan.Apply(true)
			require.NoError(t, err)
			again, err := PlanProviderSetup(root, framework, "datadoghq.com")
			require.NoError(t, err)
			require.True(t, again.Configured(), "applying again changes nothing")
			require.False(t, providerJobsMissingSetup(t, root, framework))
		})
	}
}

// providerJobsMissingSetup reports whether a job running framework's tests
// lacks a complete setup, as onboarding checks it.
func providerJobsMissingSetup(t *testing.T, root, framework string) bool {
	t.Helper()
	workflows, err := readProviderWorkflows(jenkins{}, root)
	require.NoError(t, err)
	for _, workflow := range workflows {
		for name, job := range workflow.Jobs {
			if job.SetupUnsupported != "" {
				continue // The plugin does not instrument it; it stays reported.
			}
			if problem := providerJobSetupProblem(root, jenkins{}, workflow, name, discoveryLanguageName(framework), framework, "datadoghq.com"); problem != "" {
				t.Log(name, problem)
				return true
			}
		}
	}
	return false
}

// Onboarding hands JavaScript to onboard --apply, which enables the plugin;
// a Jenkinsfile it refuses to edit gets the instructions, naming why.
func TestJenkinsJavaScriptHandsOffToApply(t *testing.T) {
	jenkinsApplyRepository(t, "jest", "pipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	useCIStatements(t, []string{"node=22"})
	var out bytes.Buffer
	require.NoError(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	require.Equal(t, "ready", result.Status)
	require.Equal(t, []string{"onboard", "--framework", "jest", "--apply", "--yes"}, result.NextCommand[1:6])

	writeFile(t, ".", "Jenkinsfile", "@Library('lib') _\npipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	out.Reset()
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	result = AgentResult{}
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	require.Equal(t, "ci_setup_required", result.Code)
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Contains(t, result.Reason, "onboard --apply cannot write this setup (Jenkinsfile:1: The Jenkinsfile loads a shared library (@Library)")
	require.Contains(t, result.Instruction, "datadog(testOptimization: [enabled: true, serviceName: '<service>', languages: ['JAVASCRIPT']])")
	require.NotContains(t, result.Instruction, "ddtest onboard --apply")
}

// Ruby's agent handoff runs onboard --apply for the Jenkinsfile edit; once
// the Jenkinsfile has it, the rest of the setup is the agent's.
func TestJenkinsRubyHandsOffToApply(t *testing.T) {
	root := jenkinsApplyRepository(t, "rspec", "node {\n  stage('Test') {\n    sh 'bundle exec rake spec'\n  }\n}\n")
	useCIStatements(t, []string{"ruby=3.3"})
	var out bytes.Buffer
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json", Site: "datadoghq.eu"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	require.Equal(t, "ci_setup_required", result.Code)
	require.Equal(t, "run_command", result.NextAction)
	require.Equal(t, []string{"onboard", "--framework", "rspec", "--apply", "--yes", "--ci-provider", "jenkins", "--ci-runtime", "ruby=3.3", "--format", "json", "--site", "datadoghq.eu"}, result.NextCommand[1:])
	require.Contains(t, result.Reason, "onboard --apply writes it")
	require.Empty(t, result.ResumeCommand, "the apply result names validation")

	instructions := providerSetupInstructions(root, jenkins{}, "ruby", "rspec", "", true)
	require.Contains(t, instructions, "`ddtest onboard --apply` previews the edit that enables the Datadog Jenkins plugin in the CI configuration, writing nothing")

	plan, err := PlanProviderSetup(root, "rspec", "")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	out.Reset()
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json", SetupApplied: true}))
	result = AgentResult{}
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	require.Equal(t, "ci_setup_required", result.Code, "the datadog-ci pin comes before the facts to confirm")
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Contains(t, result.Reason, "onboard --apply wrote the Datadog Jenkins plugin")
	require.Contains(t, result.Instruction, "Pin datadog-ci in the bundle")
	out.Reset()
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	result = AgentResult{}
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	require.Equal(t, "ci_setup_confirmation_required", result.Code)
	require.NotContains(t, providerSetupInstructions(root, jenkins{}, "ruby", "rspec", "", true), "ddtest onboard --apply", "nothing left to write")
}

// The JavaScript setup completes a datadog option that differs instead of
// leaving it for review, adds an ESM loader the option lacks, and still
// refuses what it must not write.
func TestJenkinsJavaScriptPlanCompletesTheOption(t *testing.T) {
	plan := func(framework, jenkinsfile string) (string, error) {
		root := jenkinsApplyRepository(t, framework, jenkinsfile)
		workflows, err := readProviderWorkflows(jenkins{}, root)
		require.NoError(t, err)
		setup, err := planProviderJavaScriptSetup(root, jenkins{}, workflows, framework, "6.22.0", "datadoghq.com", "")
		if err != nil {
			return "", err
		}
		for _, edit := range setup.edits {
			if edit.path == "Jenkinsfile" {
				return string(edit.after), nil
			}
		}
		return jenkinsfile, nil
	}
	option := func(arguments string) string {
		return "pipeline {\n  agent any\n  options {\n    datadog(testOptimization: [" + arguments + "])\n  }\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n"
	}
	after, err := plan("jest", option("enabled: false, serviceName: 'web', languages: ['JAVASCRIPT']"))
	require.NoError(t, err)
	require.Equal(t, option("enabled: true, serviceName: 'web', languages: ['JAVASCRIPT']"), after)

	complete := option("enabled: true, serviceName: 'web', languages: ['JAVASCRIPT']")
	after, err = plan("jest", complete)
	require.NoError(t, err)
	require.Equal(t, complete, after, "a complete setup is kept as it is")

	after, err = plan("vitest", complete)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(complete, "sh 'npm test'", "sh '"+pluginESMImportCommand+"; npm test'", 1), after)

	_, err = plan("jest", option("enabled: true, serviceName: 'web', languages: ['JAVASCRIPT'], additionalVariables: [DD_CIVISIBILITY_ENABLED: 'false']"))
	require.ErrorContains(t, err, "disables Test Optimization with DD_CIVISIBILITY_ENABLED=false")
	require.ErrorContains(t, err, "so ddtest wrote nothing")

	_, err = plan("jest", "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      options { datadog(testOptimization: [enabled: true, serviceName: 'web', languages: ['JAVASCRIPT']]) }\n      steps { sh 'npm test' }\n    }\n  }\n}\n")
	require.ErrorContains(t, err, "is in a stage's options", "a stage-level option is never certified, so the edit is refused")
}

// Without a project name, serviceName is the repository's: its origin
// remote's name, otherwise the checkout directory's.
func TestJenkinsServiceNameFallsBackToTheRepository(t *testing.T) {
	root := jenkinsApplyRepository(t, "jest", "node {\n  sh 'npm test'\n}\n")
	writeFile(t, root, "package.json", `{"scripts": {"test": "jest"}, "devDependencies": {"jest": "30.0.0"}}`)
	service := func() string {
		plan, err := PlanProviderSetup(root, "jest", "")
		require.NoError(t, err)
		require.Len(t, plan.edits, 1)
		after := string(plan.edits[0].after)
		start := strings.Index(after, "serviceName: '") + len("serviceName: '")
		return after[start : start+strings.Index(after[start:], "'")]
	}
	require.Equal(t, filepath.Base(root), service())
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:acme/web-app.git"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		require.NoError(t, command.Run())
	}
	require.Equal(t, "web-app", service())
}
