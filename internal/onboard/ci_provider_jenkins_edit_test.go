// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
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
			// JavaScript's onboard --apply selects the tracer release a
			// container stage's setup snippet installs; Ruby's is the one
			// Gemfile.lock pins.
			release := ""
			if framework != "rspec" {
				release = "6.22.0"
			}
			plan, err := planProviderSetup(root, framework, "datadoghq.com", release)
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
			require.Equal(t, "Jenkinsfile", plan.edits[0].path)
			require.Equal(t, string(want), string(plan.edits[0].after))
			if strings.Contains(string(want), jenkinsTracerFolder) {
				// The setup snippet's folders stay untracked.
				require.Len(t, plan.edits, 2)
				require.Equal(t, ".gitignore", plan.edits[1].path)
				require.Contains(t, strings.Split(string(plan.edits[1].after), "\n"), ".datadog-ci-container.*/", "at any depth: dir(…) or a cd may run the snippet below the root")
			} else {
				require.Len(t, plan.edits, 1)
			}

			_, err = plan.Apply(true)
			require.NoError(t, err)
			again, err := planProviderSetup(root, framework, "datadoghq.com", release)
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

// An existing plugin keeps its site, and validation reports a mismatch as a caveat.
func TestJenkinsJavaScriptSiteMismatchIsPreservedWithACaveat(t *testing.T) {
	root := jenkinsApplyRepository(t, "jest", "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	useCIStatements(t, []string{"node=22"}, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.eu")
	start := func(options StartOptions) AgentResult {
		t.Helper()
		var out bytes.Buffer
		_ = StartWithOptions(&out, options)
		var result AgentResult
		require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
		return result
	}

	t.Setenv("DD_SITE", "datadoghq.com")
	for name, options := range map[string]StartOptions{"DD_SITE": {Format: "json"}, "--site": {Format: "json", Site: "datadoghq.com"}} {
		result := start(options)
		require.Equal(t, "ready", result.Status, name)
		plan, err := PlanProviderSetup(root, "jest", "datadoghq.com")
		require.NoError(t, err)
		require.True(t, plan.Configured())
		check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
		}).WithSiteCaveats("datadoghq.com")
		require.Contains(t, strings.Join(CISetupCaveats(&check), "\n"), "reports to datadoghq.eu, but --site selects datadoghq.com")
	}

	t.Setenv("DD_SITE", "datadoghq.eu")
	result := start(StartOptions{Format: "json"})
	require.Equal(t, "ready", result.Status, "the plugin reports to the selected site")
	require.Equal(t, []string{"onboard", "--framework", "jest", "--apply", "--yes"}, result.NextCommand[1:6])
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

	stage := "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      options { datadog(testOptimization: [enabled: false, serviceName: 'web', languages: ['JAVASCRIPT']]) }\n      steps { sh 'npm test' }\n    }\n  }\n}\n"
	after, err = plan("jest", stage)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(stage, "enabled: false", "enabled: true", 1), after, "a stage-level option is completed in place")

	// The container stage gets the setup snippet in its sh step, which
	// withCredentials encloses: the plugin's key does not reach it.
	mixed := "pipeline {\n  agent none\n  stages {\n    stage('Docker') {\n      agent { docker { image 'node:22' } }\n      steps {\n        sh 'npm test'\n      }\n    }\n    stage('Host') {\n      agent any\n      steps { sh 'npm test' }\n    }\n  }\n}\n"
	after, err = plan("jest", mixed)
	require.NoError(t, err)
	snippet := jenkinsSnippet(jenkinsContainerRequest(t, "jest", "6.22.0"), false).shell()
	staged := strings.Replace(mixed, "      agent any\n", "      agent any\n      options {\n        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])\n      }\n", 1)
	staged = strings.Replace(staged, "        sh 'npm test'\n", "        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n            "+strings.ReplaceAll(snippet, "\n", "\n            ")+"\n            npm test\n          '''\n        }\n", 1)
	require.Equal(t, staged, after, "the host stage gets the option; the container stage the snippet, outside its scope")
	after, err = plan("jest", staged)
	require.NoError(t, err)
	require.Equal(t, staged, after, "applying again is configured")

	// A one-line steps block leaves no line to enclose: the key binding is
	// the user's, then onboard --apply writes the rest.
	_, err = plan("jest", strings.Replace(mixed, "      steps {\n        sh 'npm test'\n      }\n", "      steps { sh 'npm test' }\n", 1))
	var edit *EditThenApplyGap
	require.ErrorAs(t, err, &edit)
	require.ErrorContains(t, err, "DD_API_KEY does not reach this sh step")

	// Containers alone get the snippet too.
	containers := strings.Replace(mixed, "      agent any\n", "      agent { docker { image 'node:20' } }\n", 1)
	after, err = plan("jest", strings.Replace(containers, "      steps { sh 'npm test' }\n", "      steps {\n        sh 'npm test'\n      }\n", 1))
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(after, "withCredentials("), after)
	require.NotContains(t, after, "datadog(testOptimization")
}

// jenkinsContainerRequest is the setup a Jenkins container stage runs for
// framework with tracer release.
func jenkinsContainerRequest(t *testing.T, framework, release string) ciSetupRequest {
	t.Helper()
	request := discoveryLanguageFor(framework).ciSetupRequest(t.TempDir(), framework, "datadoghq.com", jenkinsContainer{}.setupTerms())
	request.TracerVersion, request.Framework = release, framework
	return request
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

// jenkinsPlanAfter plans the JavaScript setup of a Jenkinsfile for framework
// in root, returning the edited Jenkinsfile (unchanged when the plan edits
// none).
func jenkinsPlanAfter(t *testing.T, root, framework string) (string, error) {
	t.Helper()
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
	data, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	return string(data), nil
}

// Each sh step runs a shell of its own, so the ESM loader goes into every
// one that runs ESM tests: for Mocha, the ESM step even when a CommonJS one
// runs first, and not the CommonJS one.
func TestJenkinsESMLoaderInEveryTestShell(t *testing.T) {
	root := jenkinsApplyRepository(t, "jest", "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npx nyc mocha test/unit.js'\n        sh 'npx nyc mocha test/esm.mjs'\n      }\n    }\n  }\n}\n")
	writeFile(t, root, "package.json", `{"name": "app", "devDependencies": {"mocha": "11.0.0", "nyc": "17.0.0"}}`)
	writeFile(t, root, "test/unit.js", "it('a', () => {})\n")
	writeFile(t, root, "test/esm.mjs", "it('b', () => {})\n")
	after, err := jenkinsPlanAfter(t, root, "mocha")
	require.NoError(t, err)
	require.Contains(t, after, "        sh 'npx nyc mocha test/unit.js'\n", "a CommonJS shell needs only the preload")
	require.Contains(t, after, "        sh '"+pluginESMImportCommand+"; npx nyc mocha test/esm.mjs'\n")
}

// The check of an edited Jenkinsfile reads every test shell: a loader in
// the first one only, or after the test command, is not a complete setup,
// and onboard --apply completes it; one before the test command, after
// other statements, is kept.
func TestJenkinsSetupCheckReadsEveryTestShell(t *testing.T) {
	pipeline := func(steps ...string) string {
		return "pipeline {\n  agent any\n" + jenkinsDatadogOption + "  stages {\n    stage('Test') {\n      steps {\n        " + strings.Join(steps, "\n        ") + "\n      }\n    }\n  }\n}\n"
	}
	firstOnly := pipeline("sh '"+pluginESMImportCommand+"; npx vitest run --dir a'", "sh 'npx vitest run --dir b'")
	root := jenkinsApplyRepository(t, "vitest", firstOnly)
	workflows, err := readProviderWorkflows(jenkins{}, root)
	require.NoError(t, err)
	request := javascriptDiscovery{}.ciSetupRequest(root, "vitest", "datadoghq.com", jenkins{}.setupTerms())
	request.Framework = "vitest"
	err = checkJenkinsfileSetup(root, jenkins{}, workflows, "Jenkinsfile", firstOnly, []string{"Test"}, request)
	require.ErrorContains(t, err, "the sh step at Jenkinsfile:10 runs npx vitest run --dir b without "+pluginESMImportCommand+" in effect before it in its shell")

	// Validation reports the step as missing only the loader, which
	// onboard --apply adds.
	useCIStatements(t, []string{"node=22"})
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.22.0", Node: ">=18"}, nil
	}, "vitest")
	var codes []string
	for _, finding := range result.Jobs {
		codes = append(codes, finding.Code)
	}
	require.Equal(t, []string{"", CodeMissingInitialization}, codes, result.Jobs)
	require.Contains(t, result.Jobs[1].Reason, "does not register its ESM loader before the test command. Add "+pluginESMImportCommand)

	after, err := jenkinsPlanAfter(t, root, "vitest")
	require.NoError(t, err)
	require.Equal(t, pipeline("sh '"+pluginESMImportCommand+"; npx vitest run --dir a'", "sh '"+pluginESMImportCommand+"; npx vitest run --dir b'"), after)

	before := pipeline("sh '''\n          npm ci\n          " + pluginESMImportCommand + "\n          npx vitest run\n        '''")
	writeFile(t, root, "Jenkinsfile", before)
	after, err = jenkinsPlanAfter(t, root, "vitest")
	require.NoError(t, err)
	require.Equal(t, before, after, "a loader that runs before the test command is kept")
	plan, err := PlanProviderSetup(root, "vitest", "datadoghq.com")
	require.NoError(t, err)
	require.True(t, plan.Configured())
}

// A datadog(…) call that passes a positional map beside named arguments is
// one Jenkins rejects, so the reader never certifies it.
func TestJenkinsDatadogMixedArgumentsAreNotCertified(t *testing.T) {
	root := jenkinsApplyRepository(t, "jest", "datadog([collectLogs: true], testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']]) {\n  node {\n    stage('Test') {\n      sh 'npm test'\n    }\n  }\n}\n")
	workflows, err := readProviderWorkflows(jenkins{}, root)
	require.NoError(t, err)
	require.Len(t, workflows, 1)
	problem := providerJobSetupProblem(root, jenkins{}, workflows[0], "Test", "javascript", "jest", "datadoghq.com")
	require.Contains(t, problem, "The datadog(…) call at Jenkinsfile:1 passes a positional argument, such as a map, beside its named arguments, which Jenkins rejects")
	_, err = PlanProviderSetup(root, "jest", "datadoghq.com")
	require.ErrorContains(t, err, "positional or computed arguments")
}

// onboard --apply rejects options the handoff rejects before it writes the
// Jenkinsfile.
func TestJenkinsPluginApplyRejectsOptionsBeforeWriting(t *testing.T) {
	jenkinsfile := "node {\n  stage('Test') {\n    sh 'bundle exec rake spec'\n  }\n}\n"
	for name, options := range map[string]StartOptions{
		"command": {Format: "json", Command: "bundle exec rspec"},
		"site":    {Format: "json", Site: "datadoghq.com/evil"},
	} {
		t.Run(name, func(t *testing.T) {
			root := jenkinsApplyRepository(t, "rspec", jenkinsfile)
			useCIStatements(t, []string{"ruby=3.3"})
			var out bytes.Buffer
			require.Error(t, ApplyPluginSetup(&out, options, PluginApplyOptions{AllowDirty: true}))
			var result AgentResult
			require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
			require.Equal(t, "invalid_options", result.Code)
			require.Empty(t, result.Changes)
			data, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
			require.NoError(t, err)
			require.Equal(t, jenkinsfile, string(data), "nothing is written")
		})
	}
}

// Native ESM Cucumber on a host stage needs the Istanbul ESM loader with
// the plugin's preload: onboard --apply adds it in the test's shell, after
// the tracer's ESM loader, and the result validates.
func TestJenkinsNativeESMCucumberGetsTheIstanbulLoader(t *testing.T) {
	root := jenkinsApplyRepository(t, "jest", "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        sh 'npm test'\n      }\n    }\n  }\n}\n")
	writeFile(t, root, "package.json", `{"name": "app", "type": "module", "scripts": {"test": "cucumber-js"}, "devDependencies": {"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@istanbuljs/esm-loader-hook": "0.3.0"}}`)
	useCIStatements(t, []string{"node=22"})
	loader := pluginOptionsCommand(cucumberESMLoaderHook)
	request := javascriptDiscovery{}.ciSetupRequest(root, "cucumber", "datadoghq.com", jenkins{}.setupTerms())
	require.Equal(t, []string{pluginESMImportCommand, loader}, request.After)
	plan, err := PlanJavaScriptSetup(root, "cucumber", "6.22.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.Contains(t, plannedFile(t, plan, "Jenkinsfile"), "        sh '"+pluginESMImportCommand+"; "+loader+"; npm test'\n")
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.22.0", Node: ">=18"}, nil
	}, "cucumber")
	require.Equal(t, StatusCompatible, result.Status, result.Jobs)
	again, err := PlanJavaScriptSetup(root, "cucumber", "6.22.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured(), "a second apply changes nothing")

	// CommonJS Cucumber needs no Istanbul loader.
	writeFile(t, root, "package.json", `{"name": "app", "scripts": {"test": "cucumber-js"}, "devDependencies": {"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0"}}`)
	request = javascriptDiscovery{}.ciSetupRequest(root, "cucumber", "datadoghq.com", jenkins{}.setupTerms())
	require.Equal(t, []string{pluginESMImportCommand}, request.After)
}
