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
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jenkinsTestSnippet is the setup snippet for language's tests in a
// container stage, indented for a sh script, with reset when ddtest cannot
// prove the stage is outside the plugin's scope.
func jenkinsTestSnippet(language, release string, reset bool, after ...string) string {
	request := ciSetupRequest{Language: language, Site: "datadoghq.com", TracerInput: language + "-tracer-version", TracerVersion: release, After: after}
	return strings.ReplaceAll(jenkinsSnippet(request, reset).shell(), "\n", "\n      ")
}

// jenkinsContainerStage is a declarative pipeline whose Test stage runs
// script in agent, with the steps around the sh step given.
func jenkinsContainerStage(agent, environment, script string) string {
	return "pipeline {\n  agent { " + agent + " }\n  stages {\n    stage('Test') {\n" + environment + "      steps {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n      " + script + "\n          '''\n        }\n      }\n    }\n  }\n}\n"
}

// onboard --apply never certifies a key a file binding supplies: the edited
// Jenkinsfile's check refuses it, so nothing is written.
func TestJenkinsApplyRefusesAFileBoundKey(t *testing.T) {
	root := jenkinsApplyRepository(t, "jest", "pipeline {\n  agent { docker { image 'node:22' } }\n  stages {\n    stage('Test') {\n      steps {\n        withCredentials([file(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh 'npm test'\n        }\n      }\n    }\n  }\n}\n")
	_, err := planProviderSetup(root, "jest", "datadoghq.com", "6.22.0")
	require.ErrorContains(t, err, "so ddtest wrote nothing")
	require.ErrorContains(t, err, "withCredentials binds DD_API_KEY with file(…, variable: 'DD_API_KEY')")
}

// The setup snippet is recognized in the sh script that runs the tests of a
// container stage; the tests after it see its exports.
func TestJenkinsRecognizesTheSetupSnippet(t *testing.T) {
	snippet := jenkinsTestSnippet("js", "5.134.0", false)
	workflow := jenkinsWorkflow(t, jenkinsContainerStage("docker { image 'node:22' }", "", snippet+"\n      npm test"))
	test := workflow.Jobs["Test"]
	require.Equal(t, []string{"SETUP", "npm test"}, jenkinsRuns(test))
	setup := test.Steps[0].Setup
	require.Empty(t, setup.Problem)
	require.True(t, setup.Supported)
	require.Equal(t, map[string]string{"languages": "js", "site": "datadoghq.com", "js-tracer-version": "5.134.0", "api_key": "$DD_API_KEY"}, setup.Inputs)
	require.Equal(t, []string{jenkinsPluginOutsideCaveat}, setup.Caveats)
	require.Empty(t, workflow.Review, "outside the plugin's scope the binding is the snippet's key")
	require.Contains(t, test.Steps[1].Env["NODE_OPTIONS"], "-r ${{ env.DD_TRACE_PACKAGE }}", "the tests run with the installer's exports")
	require.Equal(t, "", jenkins{}.setupProblem(*setup, "js-tracer-version", exactRelease, "", "datadoghq.com"))
	require.Contains(t, jenkins{}.setupProblem(*setup, "js-tracer-version", exactRelease, "", "datadoghq.eu"), "The Datadog setup snippet reports to datadoghq.com, but the selected Datadog site is datadoghq.eu")

	problem := func(t *testing.T, jenkinsfile string) string {
		t.Helper()
		// The setup snippet's, after a plugin's whose scope includes it.
		found := "no setup"
		for _, step := range jenkinsWorkflow(t, jenkinsfile).Jobs["Test"].Steps {
			switch {
			case step.Setup != nil:
				found = step.Setup.Problem
			case step.ResolutionError != "":
				return "UNRESOLVED " + step.ResolutionError
			}
		}
		return found
	}
	unbound := strings.Replace(jenkinsContainerStage("docker { image 'node:22' }", "", snippet+"\n      npm test"), "withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {", "dir('.') {", 1)
	credential := strings.Replace(unbound, "      steps {\n", "      environment {\n        DD_API_KEY = credentials('datadog-api-key')\n      }\n      steps {\n", 1)
	literal := strings.Replace(unbound, "      steps {\n", "      environment {\n        DD_API_KEY = 'abc123'\n      }\n      steps {\n", 1)
	// Only a binding of a credential's secret text gives the key itself.
	binding := func(bindings string) string {
		return strings.Replace(jenkinsContainerStage("docker { image 'node:22' }", "", snippet+"\n      npm test"), "[string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]", bindings, 1)
	}
	const notTheKey = "withCredentials binds DD_API_KEY with "
	for name, test := range map[string]struct{ jenkinsfile, problem string }{
		"no key":                 {unbound, "DD_API_KEY does not reach this sh step, so the setup snippet always skips"},
		"credentials()":          {credential, ""},
		"literal key":            {literal, "DD_API_KEY is written into the Jenkinsfile for this step"},
		"cleared key":            {jenkinsContainerStage("docker { image 'node:22' }", "", "unset DD_API_KEY\n      "+snippet+"\n      npm test"), "An earlier command of the sh script clears DD_API_KEY"},
		"Alpine":                 {jenkinsContainerStage("docker { image 'node:22-alpine' }", "", snippet+"\n      npm test"), "The step's image node:22-alpine has no Bash"},
		"slim":                   {jenkinsContainerStage("docker { image 'node:22-slim' }", "", snippet+"\n      npm test"), "has neither curl nor wget"},
		"slim with curl":         {jenkinsContainerStage("docker { image 'node:22-slim' }", "", "apt-get update && apt-get install -y curl\n      "+snippet+"\n      npm test"), ""},
		"Buildkite's form":       {jenkinsContainerStage("docker { image 'node:22' }", "", strings.ReplaceAll(buildkiteRequestSnippet(ciSetupRequest{Language: "js", Site: "datadoghq.com", TracerInput: "js-tracer-version", TracerVersion: "5.134.0"}).shell(), "\n", "\n      ")+"\n      npm test"), "UNRESOLVED Jenkinsfile Test: The step runs Datadog's installer in a form ddtest does not recognize"},
		"file binding":           {binding("[file(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]"), notTheKey + "file(…, variable: 'DD_API_KEY'), which sets it to a file's path"},
		"file binding by class":  {binding("[[$class: 'FileBinding', credentialsId: 'datadog-api-key', variable: 'DD_API_KEY']]"), notTheKey + "FileBinding(…, variable: 'DD_API_KEY')"},
		"user name binding":      {binding("[usernamePassword(credentialsId: 'datadog', usernameVariable: 'DD_API_KEY', passwordVariable: 'PASSWORD')]"), notTheKey + "usernamePassword(…, usernameVariable: 'DD_API_KEY')"},
		"user:password binding":  {binding("[usernameColonPassword(credentialsId: 'datadog', variable: 'DD_API_KEY')]"), notTheKey + "usernameColonPassword(…, variable: 'DD_API_KEY')"},
		"password binding":       {binding("[usernamePassword(credentialsId: 'datadog', usernameVariable: 'USER', passwordVariable: 'DD_API_KEY')]"), ""},
		"secret text by class":   {binding("[[$class: 'StringBinding', credentialsId: 'datadog-api-key', variable: 'DD_API_KEY']]"), ""},
		"secret text after file": {binding("[file(credentialsId: 'settings', variable: 'SETTINGS'), string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]"), ""},
		"plugin's scope":         {strings.Replace(jenkinsContainerStage("docker { image 'node:22' }", "", snippet+"\n      npm test"), "  stages {\n", jenkinsDatadogOption+"  stages {\n", 1), "The setup snippet runs in the scope of the datadog(…) option at Jenkinsfile:4"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, problem(t, test.jenkinsfile), test.problem)
			if test.problem == "" {
				require.Empty(t, problem(t, test.jenkinsfile))
			}
		})
	}
}

// A datadog(…) call ddtest cannot read may reach a container block: its
// snippet must first remove the variables the plugin would set there.
func TestJenkinsSnippetResetsWhereThePluginMayReach(t *testing.T) {
	scripted := func(snippet string) string {
		return "def options = [testOptimization: [enabled: true]]\ndatadog(options) {\n  node('docker') {\n    stage('Test') {\n      docker.image('node:22').inside {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n      " + snippet + "\n      npm test\n          '''\n        }\n      }\n    }\n  }\n}\n"
	}
	jobs := jenkinsJobs(t, scripted(jenkinsTestSnippet("js", "5.134.0", false)))
	inside := jobs["Test (docker.image('node:22').inside)"]
	require.Equal(t, jenkinsContainer{reset: true}, inside.SetupProvider)
	require.Contains(t, inside.Steps[0].Setup.Problem, "ddtest cannot tell whether the datadog(…) call at Jenkinsfile:2 reaches this step")
	require.Contains(t, inside.Steps[0].Setup.Problem, (javascriptDiscovery{}).ciPluginReset())

	jobs = jenkinsJobs(t, scripted(jenkinsTestSnippet("js", "5.134.0", true)))
	inside = jobs["Test (docker.image('node:22').inside)"]
	require.Equal(t, []string{"SETUP", "npm test"}, jenkinsRuns(inside))
	require.Empty(t, inside.Steps[0].Setup.Problem)
	require.NotContains(t, inside.Steps[0].Setup.Caveats, jenkinsPluginOutsideCaveat)

	// The instructions write the reset for those stages.
	request := javascriptDiscovery{}.ciSetupRequest(t.TempDir(), "jest", "datadoghq.com", jenkinsContainer{reset: true}.setupTerms())
	text := jenkinsContainer{reset: true}.setupInstructions(request)
	require.Contains(t, text, (javascriptDiscovery{}).ciPluginReset()+"\nif [ \"${DD_API_KEY:+set}\" = set ]; then")
	require.Contains(t, text, "the snippet first removes the variables the plugin would set there")
	require.NotContains(t, jenkinsContainer{}.setupInstructions(request), (javascriptDiscovery{}).ciPluginReset())
	require.Equal(t, []string{(rubyDiscovery{}).ciPluginReset()}, jenkinsPluginResets("ruby"))

	// Another language's reset leaves JavaScript's plugin variables in place.
	jobs = jenkinsJobs(t, scripted((rubyDiscovery{}).ciPluginReset()+"\n      "+jenkinsTestSnippet("js", "5.134.0", false)))
	inside = jobs["Test (docker.image('node:22').inside)"]
	require.Contains(t, inside.Steps[0].Setup.Problem, "ddtest cannot tell whether the datadog(…) call at Jenkinsfile:2 reaches this step")
	require.Contains(t, inside.Steps[0].Setup.Problem, (javascriptDiscovery{}).ciPluginReset())
}

// An agent { dockerfile … } runs the image its Dockerfile builds, whose
// base image gives the runtime and whose RUN instructions may install what
// the installer needs.
func TestJenkinsDockerfileAgents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Dockerfile.ci", "ARG RUBY_VERSION=3.2.9\nFROM docker.io/library/ruby:$RUBY_VERSION-slim\nRUN apt-get update && apt-get install -y curl\n")
	writeFile(t, root, "ci/Dockerfile", "FROM node:22-alpine\n")
	workflow, err := readJenkinsfile(root, "Jenkinsfile", "pipeline {\n  agent none\n  stages {\n    stage('Ruby') {\n      agent { dockerfile { filename 'Dockerfile.ci' } }\n      steps { sh 'bundle exec rake test' }\n    }\n    stage('Node') {\n      agent { dockerfile { dir 'ci' } }\n      steps { sh 'npm test' }\n    }\n    stage('Arguments') {\n      agent { dockerfile { filename 'Dockerfile.ci'\n        additionalBuildArgs '--build-arg RUBY_VERSION=3.3' } }\n      steps { sh 'npm test' }\n    }\n  }\n}\n")
	require.NoError(t, err)
	ruby := workflow.Jobs["Ruby"]
	require.Equal(t, "docker.io/library/ruby:3.2.9-slim", ruby.Image)
	require.Empty(t, ruby.SetupImageProblem, "its Dockerfile installs curl")
	node := workflow.Jobs["Node"]
	require.Equal(t, "node:22-alpine", node.Image)
	require.Empty(t, node.SetupImageProblem, "it takes the setup without Bash or curl")
	fallback, ok := fallbackJob(node)
	require.True(t, ok, "its image has no Bash")
	require.Equal(t, "no Bash", fallback.missing)
	require.True(t, jenkinsContainerJob(node))
	arguments := workflow.Jobs["Arguments"]
	require.Empty(t, arguments.Image, "the build overrides the argument the base image reads")
	require.Contains(t, arguments.Host, "the container of agent { dockerfile … } (Dockerfile.ci), where its Dockerfile Dockerfile.ci")

	// --target builds that stage, not the last one; options ddtest cannot
	// follow leave the image unresolved, for the user to state.
	writeFile(t, root, "Dockerfile.stages", "FROM node:18-bookworm AS tests\nFROM node:22-bookworm\n")
	agent := func(arguments string) string {
		return "pipeline {\n  agent { dockerfile { filename 'Dockerfile.stages'\n    additionalBuildArgs " + arguments + " } }\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n"
	}
	for _, arguments := range []string{"'--target tests'", "'--pull --target=tests --build-arg A=$B'"} {
		workflow, err := readJenkinsfile(root, "Jenkinsfile", agent(arguments))
		require.NoError(t, err)
		require.Equal(t, "node:18-bookworm", workflow.Jobs["Test"].Image, arguments)
	}
	for arguments, problem := range map[string]string{
		"'--target missing'":    "its Dockerfile Dockerfile.stages, which has no stage missing for the build's target",
		"'--target $STAGE'":     "its additionalBuildArgs --target $STAGE read the shell's environment, which ddtest cannot resolve",
		"\"${env.BUILD_ARGS}\"": "its additionalBuildArgs \"${env.BUILD_ARGS}\" cannot be resolved",
		"'-f Dockerfile.other'": "its additionalBuildArgs select another Dockerfile (-f)",
		"'--target'":            "its additionalBuildArgs end with --target and no value",
		// The shell removes the quotes before docker build reads the
		// argument's name.
		"'--build-arg \"NODE_VERSION=18\"'": "its additionalBuildArgs --build-arg \"NODE_VERSION=18\" use shell quoting or escapes, which ddtest cannot resolve",
		"'--target \\\\tests'":              "its additionalBuildArgs --target \\tests use shell quoting or escapes",
	} {
		workflow, err := readJenkinsfile(root, "Jenkinsfile", agent(arguments))
		require.NoError(t, err)
		test := workflow.Jobs["Test"]
		require.Empty(t, test.Image, arguments)
		require.Contains(t, test.Host, "the container of agent { dockerfile … } (Dockerfile.stages), where "+problem, arguments)
	}
}

// The runtime of a container whose image ddtest does not read is the
// user's to state, as a host agent's; the snippet's caveat says what ddtest
// could not check.
func TestJenkinsUnreadContainerImagesTakeTheStatedRuntime(t *testing.T) {
	root := jenkinsRepository(t, jenkinsContainerStage("kubernetes { yamlFile 'pod.yaml' }", "", jenkinsTestSnippet("js", "5.134.0", false)+"\n      npm test"))
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{"the container of agent { kubernetes … }, whose image ddtest does not read"}, required.Hosts)
	useCIStatements(t, []string{"node=22"})
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	result := checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, action, version string) (tracerRequirement, error) {
		require.Equal(t, "Datadog installer "+buildkiteInstaller, action)
		require.Equal(t, "5.134.0", version)
		return tracerRequirement{Version: "5.134.0", Node: ">=18"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, result.Status, result)
	require.True(t, result.Jobs[0].UserStated)
	require.Contains(t, strings.Join(CISetupCaveats(&result), "\n"), "The setup snippet runs in agent { kubernetes … }, and ddtest does not read its image: ddtest took its runtime from the user (--ci-runtime)")
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"), "the snippet needs no plugin facts")
}

// A Jenkins container's floating image tag runs the release the agent kept
// (Docker Pipeline pulls an image only when the agent has none), so, unlike
// a GitLab or Buildkite image's, its runtime is the user's to state, as a
// host agent's; a numeric tag still selects its own.
func TestJenkinsFloatingImageTagsTakeTheStatedRuntime(t *testing.T) {
	snippet := jenkinsTestSnippet("js", "5.134.0", false)
	stage := func(name, agent string) string {
		return "    stage('" + name + "') {\n      agent { " + agent + " }\n      steps {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n      " + snippet + "\n      npm test\n          '''\n        }\n      }\n    }\n"
	}
	inside := "    stage('Inside') {\n      agent any\n      steps {\n        script {\n          docker.image('node:current').inside {\n            withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n              sh '''\n      " + snippet + "\n      npm test\n              '''\n            }\n          }\n        }\n      }\n    }\n"
	root := jenkinsRepository(t, "pipeline {\n  agent none\n  stages {\n"+stage("LTS", "docker { image 'node:lts'\n        args '-u root:sudo' }")+stage("Latest", "docker { image 'node:latest' }")+stage("Untagged", "docker { image 'node' }")+stage("Pinned", "docker { image 'node:22' }")+inside+"  }\n}\n")
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{"Jenkinsfile / Inside (docker.image('node:current').inside)", "Jenkinsfile / LTS", "Jenkinsfile / Latest", "Jenkinsfile / Untagged"}, required.Jobs)
	require.Equal(t, "image node:lts, whose name selects no Node version", required.Hosts[1])
	result := CIRuntimeRequiredResult(required)
	require.Equal(t, "ci_runtime_required", result.Code)
	require.Equal(t, "ask_user", result.NextAction)
	require.Contains(t, result.RequiredInput, "ci_runtime:Jenkinsfile / LTS")

	resolve := func(_ context.Context, action, version string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.134.0", Node: ">=18"}, nil
	}
	findings := func() map[string]RuntimeFinding {
		found := map[string]RuntimeFinding{}
		for _, finding := range checkCIRuntimes(t.Context(), root, nil, resolve, "jest").Jobs {
			found[finding.Job] = finding
		}
		return found
	}
	require.Equal(t, CodeRuntimeUnknown, findings()["LTS"].Code, "unstated, it stays unknown")

	// A statement for the job is not a conflict with its floating tag.
	useCIStatements(t, []string{"LTS:node=24", "node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	stated := findings()
	require.Equal(t, StatusCompatible, stated["LTS"].Status, stated["LTS"])
	require.Equal(t, "24", stated["LTS"].Node)
	require.True(t, stated["LTS"].UserStated)
	require.Equal(t, "22", stated["Untagged"].Node)
	require.True(t, stated["Untagged"].UserStated)
	require.False(t, stated["Pinned"].UserStated, "node:22 selects its own runtime")
	useCIStatements(t, []string{"Pinned:node=24"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Node 22, which image node:22 selects")
}

// The same holds for a Ruby image's floating tag in a Jenkins container.
func TestJenkinsFloatingRubyImageTagTakesTheStatedRuntime(t *testing.T) {
	root := jenkinsApplyRepository(t, "rspec", "pipeline {\n  agent { docker { image 'ruby:latest' } }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'bundle exec rake spec'\n      }\n    }\n  }\n}\n")
	required := RequiredCIRuntimes(t.Context(), root, "ruby", "rspec")
	require.NotNil(t, required)
	require.Equal(t, []string{"Jenkinsfile / Test"}, required.Jobs)
	require.Equal(t, []string{"image ruby:latest, whose name selects no Ruby version"}, required.Hosts)
	useCIStatements(t, []string{"ruby=3.3"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec"))
	for _, finding := range RubyCIConfiguration(root, "rspec").Jobs {
		require.Equal(t, "3.3", finding.Runtime, finding)
		require.True(t, finding.UserStated)
	}
}

// Ruby with the plugin on host stages and the snippet in container stages:
// the plugin's bundle add would change the release the installer uses, so
// Gemfile.lock must pin datadog-ci, at the snippet's release.
func TestJenkinsRubySnippetBesideThePlugin(t *testing.T) {
	root := copyFixture(t, "jenkins-ruby")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	snippet := jenkinsTestSnippet("ruby", "1.42.0", false)
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent none\n  stages {\n    stage('Host') {\n      agent { label 'linux' }\n      options {\n        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']])\n      }\n      steps { sh 'bundle exec rake spec' }\n    }\n    stage('Docker') {\n      agent { docker { image 'ruby:3.3' } }\n      steps {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n      "+snippet+"\n      bundle exec rake spec\n          '''\n        }\n      }\n    }\n  }\n}\n")
	reason := func() string {
		for _, job := range RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs {
			if job.Job == "Docker" {
				return job.Reason
			}
		}
		return ""
	}
	require.Contains(t, reason(), "The build also runs the Datadog Jenkins plugin, which the datadog(…) option at Jenkinsfile:7 enables, for Ruby in the same workspace: without datadog-ci pinned in Gemfile.lock")
	// Local validation's copy of the checkout gains datadog-ci in its
	// bundle; the pin that counts is the checkout's.
	copied := t.TempDir()
	writeFile(t, copied, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.42.0)\n")
	writeFile(t, copied, "Jenkinsfile", readTestFile(t, root, "Jenkinsfile"))
	for _, job := range RubyCIConfigurationFrom(copied, root, "rspec", "datadoghq.com").Jobs {
		require.Equal(t, CodeIncompleteCISetup, job.Code, job.Job)
	}
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.41.0)\n")
	require.Equal(t, `Gemfile.lock pins datadog-ci 1.41.0, which the installer uses instead of the setup snippet's 1.42.0; set DD_SET_TRACER_VERSION_RUBY="1.41.0" in it.`, reason())
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.42.0)\n")
	require.NotContains(t, reason(), "Gemfile.lock")

	// The instructions for the container stage ask for the same pin.
	text := providerSetupInstructions(root, jenkins{}, "ruby", "rspec", "datadoghq.com", true)
	require.Contains(t, text, `DD_SET_TRACER_VERSION_RUBY="1.42.0"`, "the snippet installs the pinned release")
	require.Contains(t, text, "Gemfile.lock pins datadog-ci 1.42.0, which the installer uses as is, and the build also runs the Datadog Jenkins plugin of Jenkinsfile / Host")
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    rake (13.4.2)\n")
	text = providerSetupInstructions(root, jenkins{}, "ruby", "rspec", "datadoghq.com", true)
	require.Contains(t, text, "pin datadog-ci exactly in the Gemfile and Gemfile.lock, as the instructions for those stages show")
}

// A datadog(…) call ddtest cannot read may enable the plugin for Ruby: the
// snippet's reset removes the plugin's variables in its shell, but not the
// bundle add the plugin may have run in the workspace, so Gemfile.lock must
// pin datadog-ci there too.
func TestJenkinsRubySnippetUnderAnUnreadPluginCall(t *testing.T) {
	root := copyFixture(t, "jenkins-ruby")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	snippet := jenkinsTestSnippet("ruby", "1.42.0", true)
	shell := "withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n      " + snippet + "\n      bundle exec rake spec\n          '''\n        }"
	for name, jenkinsfile := range map[string]string{
		"positional map":    "def options = [testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]]\ndatadog(options) {\n  node('docker') {\n    stage('Test') {\n      docker.image('ruby:3.3').inside {\n        " + shell + "\n      }\n    }\n  }\n}\n",
		"computed settings": "def settings = [enabled: true, serviceName: 'app', languages: ['RUBY']]\ndatadog(testOptimization: settings) {\n  node('docker') {\n    stage('Test') {\n      docker.image('ruby:3.3').inside {\n        " + shell + "\n      }\n    }\n  }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    rake (13.4.2)\n")
			writeFile(t, root, "Jenkinsfile", jenkinsfile)
			reason := func() string {
				jobs := RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs
				require.Len(t, jobs, 1)
				return jobs[0].Reason
			}
			require.Contains(t, reason(), "The build also runs the Datadog Jenkins plugin, which the datadog(…) call at Jenkinsfile:2 may enable (ddtest cannot read for which languages), for Ruby in the same workspace: without datadog-ci pinned in Gemfile.lock")
			writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.42.0)\n")
			require.NotContains(t, reason(), "Gemfile.lock")
			require.NotContains(t, reason(), "may enable")
		})
	}

	// The instructions for such stages ask for the pin.
	request := rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", jenkinsContainer{reset: true}.setupTerms())
	require.Equal(t, "1.42.0", request.TracerVersion, "the snippet installs the pinned release")
	require.Contains(t, request.Notes[0], "the build also runs the Datadog Jenkins plugin, which a datadog(…) call ddtest cannot read may enable: keep the pin")
}

// onboard --apply for Ruby writes the plugin's option and the snippet with
// the release Gemfile.lock pins; without the pin it asks for it first, since
// the plugin beside the snippet would change the release.
func TestJenkinsRubyApplyWritesTheSnippetWithThePin(t *testing.T) {
	jenkinsfile := "pipeline {\n  agent none\n  stages {\n    stage('Host') {\n      agent { label 'linux' }\n      steps { sh 'bundle exec rake spec' }\n    }\n    stage('Docker') {\n      agent { docker { image 'ruby:3.3' } }\n      steps {\n        sh 'bundle exec rake spec'\n      }\n    }\n  }\n}\n"
	root := jenkinsApplyRepository(t, "rspec", jenkinsfile)
	_, err := PlanProviderSetup(root, "rspec", "datadoghq.com")
	var edit *EditThenApplyGap
	require.ErrorAs(t, err, &edit)
	require.ErrorContains(t, err, "Pin the tracer in the project's dependencies, as the setup instructions show, then rerun onboard --apply")
	require.False(t, pluginSetupWritable(root, "ruby", "rspec", "datadoghq.com"))
	useCIStatements(t, []string{"ruby=3.3"})
	var out bytes.Buffer
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	require.Equal(t, "ci_setup_required", result.Code)
	require.Contains(t, result.Reason, "onboard --apply writes the CI configuration's part of this setup once one edit is made (Jenkinsfile / Docker: The setup snippet for this container stage installs an exact tracer release")

	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.42.0)\n    rake (13.4.2)\n")
	plan, err := PlanProviderSetup(root, "rspec", "datadoghq.com")
	require.NoError(t, err)
	after := string(plan.edits[0].after)
	require.Contains(t, after, "      options {\n        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']])\n      }\n")
	require.Contains(t, after, `DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES="ruby" DD_SET_TRACER_VERSION_RUBY="1.42.0"`)
	require.Contains(t, after, "withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {")
	_, err = plan.Apply(true)
	require.NoError(t, err)
	again, err := PlanProviderSetup(root, "rspec", "datadoghq.com")
	require.NoError(t, err)
	require.True(t, again.Configured())

	// Container stages alone need no pin: the snippet is the instructions'.
	writeFile(t, root, "Jenkinsfile", strings.Replace(jenkinsfile, "      agent { label 'linux' }\n", "      agent { docker { image 'ruby:3.3' } }\n", 1))
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    rake (13.4.2)\n")
	_, err = PlanProviderSetup(root, "rspec", "datadoghq.com")
	require.ErrorContains(t, err, "Add the setup snippet by hand with an exact release")
	require.NotErrorAs(t, err, &edit)
}

// Each setup mechanism asks for the key its own way.
func TestJenkinsAPIKeyActions(t *testing.T) {
	useCIProvider(t, "jenkins")
	plugin := jenkins{}.apiKeyAction(false)
	snippet := jenkinsContainer{}.apiKeyAction(false)
	require.Equal(t, plugin, CIProviderAPIKeyActionFor(&RuntimeCheck{Jobs: []RuntimeFinding{{Action: jenkinsPluginReference}}}, false))
	require.Equal(t, snippet, CIProviderAPIKeyActionFor(&RuntimeCheck{Jobs: []RuntimeFinding{{Action: "Datadog installer " + buildkiteInstaller}}}, false))
	require.Equal(t, plugin+" "+snippet, CIProviderAPIKeyActionFor(&RuntimeCheck{Jobs: []RuntimeFinding{{Action: jenkinsPluginReference}, {Action: "Datadog installer " + buildkiteInstaller}}}, false))
	require.Contains(t, snippet, "Secret text (ID datadog-api-key")
	useCIProvider(t, "buildkite")
	require.Equal(t, buildkite{}.apiKeyAction(true), CIProviderAPIKeyActionFor(&RuntimeCheck{}, true), "a provider with one mechanism keeps its action")
}

// The Jenkins form differs from Buildkite's only where Jenkins needs it.
func TestJenkinsSnippetForm(t *testing.T) {
	request := ciSetupRequest{Language: "js", Site: "datadoghq.com", TracerInput: "js-tracer-version", TracerVersion: "5.134.0"}
	jenkins, buildkite := jenkinsSnippet(request, false).shell(), buildkiteRequestSnippet(request).shell()
	require.True(t, strings.HasPrefix(jenkins, `if [ "${DD_API_KEY:+set}" = set ]; then`))
	require.True(t, strings.HasPrefix(buildkite, `if [ -n "${DD_API_KEY:-}" ]; then`))
	require.Contains(t, jenkins, `bash "$DD_TRACER_FOLDER/install_test_visibility.sh" >"$DD_TRACER_FOLDER/test-optimization.env"`)
	require.Contains(t, jenkins, `DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="jenkins"`)
	require.NotContains(t, jenkins, "$$")
	require.NotContains(t, jenkins, `\`, "a single-quoted Groovy string keeps it as written")
	require.Equal(t, strings.Count(jenkins, "\n"), strings.Count(buildkite, "\n"))
	file, parsed := parseCIShell(jenkins + "\nnpm test")
	require.True(t, parsed)
	setup, consumed, ok := snippetSetupAt(file.Stmts, 0, nil, jenkinsSnippetForm, nil)
	require.True(t, ok)
	require.Equal(t, 1, consumed)
	require.True(t, setup.shaped)
	_, _, ok = snippetSetupAt(file.Stmts, 0, nil, buildkiteSnippetForm, nil)
	require.True(t, ok, "Buildkite's form reads it as an installer it does not recognize")
}

// readTestFile reads a file of root.
func readTestFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	require.NoError(t, err)
	return string(data)
}

// The setup snippet's exports reach only the sh step it runs in, each a
// shell of its own: a container test shell without its own snippet is not
// set up, whatever another sh step of the stage runs, and onboard --apply
// adds the snippet to that one only.
func TestJenkinsSnippetInEveryContainerTestShell(t *testing.T) {
	snippet := jenkinsTestSnippet("js", "6.22.0", false)
	firstOnly := "pipeline {\n  agent { docker { image 'node:22' } }\n  stages {\n    stage('Test') {\n      steps {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n      " + snippet + "\n      npm test -- packages/a\n          '''\n          sh 'npm test -- packages/b'\n        }\n      }\n    }\n  }\n}\n"
	root := jenkinsApplyRepository(t, "jest", firstOnly)
	workflows, err := readProviderWorkflows(jenkins{}, root)
	require.NoError(t, err)
	problem := providerJobSetupProblem(root, jenkins{}, workflows[0], "Test", "javascript", "jest", "datadoghq.com")
	require.Contains(t, problem, "runs in another shell than the test command at Jenkinsfile:")
	require.Contains(t, problem, "each shell that runs the tests needs it before its test command")

	plan, err := planProviderSetup(root, "jest", "datadoghq.com", "6.22.0")
	require.NoError(t, err)
	require.Len(t, plan.edits, 2)
	require.Equal(t, ".gitignore", plan.edits[1].path)
	require.Equal(t, ".datadog-ci-container.*/\n", string(plan.edits[1].after), "the snippet's folders stay untracked")
	after := string(plan.edits[0].after)
	require.Equal(t, 2, strings.Count(after, "install_test_visibility.sh\" >\"$DD_TRACER_FOLDER/test-optimization.env\""), "the second shell gets a snippet; the first keeps its own")
	require.True(t, strings.HasPrefix(after, firstOnly[:strings.Index(firstOnly, "          sh 'npm test -- packages/b'")]), "the first sh step is unchanged")
	_, err = plan.Apply(true)
	require.NoError(t, err)
	workflows, err = readProviderWorkflows(jenkins{}, root)
	require.NoError(t, err)
	require.Empty(t, providerJobSetupProblem(root, jenkins{}, workflows[0], "Test", "javascript", "jest", "datadoghq.com"))

	// A snippet in an sh step of its own sets up no later test shell.
	alone := strings.Replace(firstOnly, "\n      npm test -- packages/a\n", "\n", 1)
	writeFile(t, root, "Jenkinsfile", alone)
	workflows, err = readProviderWorkflows(jenkins{}, root)
	require.NoError(t, err)
	require.Contains(t, providerJobSetupProblem(root, jenkins{}, workflows[0], "Test", "javascript", "jest", "datadoghq.com"), "runs in another shell")
}

// jenkinsContainerShells is a declarative pipeline whose Test stage runs
// each script in an sh step of its own, in a node:22 container.
func jenkinsContainerShells(scripts ...string) string {
	steps := ""
	for _, script := range scripts {
		steps += "          sh '''\n      " + script + "\n          '''\n"
	}
	return "pipeline {\n  agent { docker { image 'node:22' } }\n  stages {\n    stage('Test') {\n      steps {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n" + steps + "        }\n      }\n    }\n  }\n}\n"
}

// The JavaScript plan reads a container stage shell by shell, as the setup
// check does: a snippet sets up only the test commands of its own sh step,
// so a test shell without one gets its own, the plan's own output is
// configured, and an existing snippet without the ESM loader is refused,
// naming the snippet's own command.
func TestJenkinsJavaScriptPlanReadsEveryContainerTestShell(t *testing.T) {
	plan := func(framework, jenkinsfile string) (string, *SetupPlan, error) {
		root := jenkinsApplyRepository(t, framework, jenkinsfile)
		workflows, err := readProviderWorkflows(jenkins{}, root)
		require.NoError(t, err)
		setup, err := planProviderJavaScriptSetup(root, jenkins{}, workflows, framework, "6.22.0", "datadoghq.com", "")
		return root, setup, err
	}
	jenkinsFile := func(setup *SetupPlan) string {
		for _, edit := range setup.edits {
			if edit.path == "Jenkinsfile" {
				return string(edit.after)
			}
		}
		return ""
	}
	snippet := jenkinsTestSnippet("js", "6.22.0", false)

	// Its own multi-shell output is configured.
	own, err := os.ReadFile(filepath.Join("testdata", "jenkins-apply", "container-every-test-shell", "after.groovy"))
	require.NoError(t, err)
	root, setup, err := plan("jest", string(own))
	require.NoError(t, err)
	require.True(t, setup.Configured())
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))

	// A snippet in the first test shell only, or in a shell of its own,
	// leaves a test shell to set up.
	for _, jenkinsfile := range []string{
		jenkinsContainerShells(snippet+"\n      npm test -- packages/a", "npm test -- packages/b"),
		jenkinsContainerShells(snippet, "npm test"),
	} {
		root, setup, err := plan("jest", jenkinsfile)
		require.NoError(t, err)
		require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"), "onboarding does not report it set up either")
		require.False(t, setup.Configured())
		after := jenkinsFile(setup)
		require.Equal(t, 2, strings.Count(after, `DD_SET_TRACER_VERSION_JS="6.22.0"`), "the test shell without a snippet gets its own")
		require.Equal(t, 1, strings.Count(after, "withCredentials("), "the binding around it already reaches it")
		for _, edit := range setup.edits {
			if edit.path == ".gitignore" {
				require.Equal(t, "/.testoptimization/\n.datadog-ci-container.*/\n", string(edit.after), "ddtest's reports and the snippet's folders stay untracked")
			}
		}
		require.True(t, slices.ContainsFunc(setup.edits, func(edit setupEdit) bool { return edit.path == ".gitignore" }))
		_, err = setup.Apply(true)
		require.NoError(t, err)
		require.False(t, providerJobsMissingSetup(t, root, "jest"))
		workflows, err := readProviderWorkflows(jenkins{}, root)
		require.NoError(t, err)
		again, err := planProviderJavaScriptSetup(root, jenkins{}, workflows, "jest", "6.22.0", "datadoghq.com", "")
		require.NoError(t, err)
		require.True(t, again.Configured())
	}

	// A snippet after a test command of its own shell comes too late.
	_, _, err = plan("jest", jenkinsContainerShells("npm test -- packages/a\n      "+snippet+"\n      npm test -- packages/b"))
	require.ErrorContains(t, err, "The Datadog setup snippet runs after the first jest test command")

	// Vitest's tests need the snippet's ESM loader in their shell.
	esm := jenkinsContainerRequest(t, "vitest", "6.22.0").After
	require.NotEmpty(t, esm)
	_, _, err = plan("vitest", jenkinsContainerShells(snippet+"\n      npm test"))
	require.ErrorContains(t, err, "so ddtest wrote nothing")
	require.ErrorContains(t, err, "runs npm test without "+strings.Join(esm, "; ")+" in effect before it in its shell")
	_, setup, err = plan("vitest", jenkinsContainerShells(jenkinsTestSnippet("js", "6.22.0", false, esm...)+"\n      npm test"))
	require.NoError(t, err)
	require.True(t, setup.Configured())
}

// The snippet creates its folder in the sh step's working directory, which
// dir(…) or a cd may put below the checkout root, so only a pattern that
// matches at any depth keeps it untracked.
func TestJenkinsTracerFolderIgnoreCoversEveryDepth(t *testing.T) {
	require.Equal(t, ".datadog-ci-container.*/", jenkinsTracerIgnore)
	for line, covered := range map[string]bool{
		".datadog-ci-container.*/":     true,
		".datadog-ci-container*":       true,
		"**/.datadog-ci-container.*/":  true,
		".datadog-ci-*":                true,
		"/.datadog-ci-container*":      false,
		"/.datadog-ci-container.*/":    false,
		"app/.datadog-ci-container.*/": false,
		"!.datadog-ci-container*":      false,
		"# .datadog-ci-container*":     false,
		".datadog":                     false,
		"":                             false,
	} {
		require.Equal(t, covered, ignoreCovers(line, jenkinsTracerIgnore), line)
	}
	// A root entry is covered at the root or at any depth.
	require.True(t, ignoreCovers("/.datadog-ci-container*", "/.datadog-ci-container.*/"))
	require.True(t, ignoreCovers(".datadog-ci-container*", "/.datadog-ci-container.*/"))

	root := t.TempDir()
	writeFile(t, root, ".gitignore", "/.datadog-ci-container*\n")
	edits, err := ignoreEdits(root, []string{jenkinsTracerIgnore})
	require.NoError(t, err)
	require.Len(t, edits, 1)
	require.Equal(t, "/.datadog-ci-container*\n.datadog-ci-container.*/\n", string(edits[0].after), "a root-only pattern misses the folders below the root")
	writeFile(t, root, ".gitignore", "**/.datadog-ci-container.*\n")
	edits, err = ignoreEdits(root, []string{jenkinsTracerIgnore})
	require.NoError(t, err)
	require.Empty(t, edits)
}

// Ruby's snippet exports RUBYOPT in its own shell only, too.
func TestJenkinsRubySnippetInEveryContainerTestShell(t *testing.T) {
	jenkinsfile := "pipeline {\n  agent { docker { image 'ruby:3.3' } }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'bundle exec rake spec'\n        sh 'bundle exec rake spec SPEC=spec/calculator_spec.rb'\n      }\n    }\n  }\n}\n"
	root := jenkinsApplyRepository(t, "rspec", jenkinsfile)
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.42.0)\n    rake (13.4.2)\n")
	writeFile(t, root, ".gitignore", "/vendor/\n.datadog-ci-container*\n")
	plan, err := planProviderSetup(root, "rspec", "datadoghq.com", "")
	require.NoError(t, err)
	require.Len(t, plan.edits, 1, "a pattern that covers the snippet's folders needs no entry")
	after := string(plan.edits[0].after)
	require.Equal(t, 2, strings.Count(after, `DD_SET_TRACER_VERSION_RUBY="1.42.0"`), after)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	again, err := planProviderSetup(root, "rspec", "datadoghq.com", "")
	require.NoError(t, err)
	require.True(t, again.Configured())
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))

	// A snippet in an sh step of its own sets up no later test shell: its
	// setup ends with its shell, so neither onboarding nor validation counts
	// the tests as instrumented.
	snippet := jenkinsTestSnippet("ruby", "1.42.0", false)
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent { docker { image 'ruby:3.3' } }\n  stages {\n    stage('Test') {\n      steps {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n      "+snippet+"\n          '''\n          sh 'bundle exec rake spec'\n        }\n      }\n    }\n  }\n}\n")
	jobs := RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs
	require.Len(t, jobs, 1)
	require.Empty(t, jobs[0].Action)
	require.Equal(t, "inconclusive", jobs[0].Status)
	require.Equal(t, "The Datadog setup snippet at Jenkinsfile:7 runs in another shell than this test command, and its exports reach only the later commands of its own shell; run it in the same shell, before the test command.", jobs[0].Reason)
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
}

// A container stage whose image cannot run the installer gets the setup
// without Bash or curl in its Jenkins form, not the Datadog Jenkins plugin
// of the host agents, and no image note.
func TestJenkinsContainerImageNoteNamesTheSnippet(t *testing.T) {
	for _, tc := range []struct{ framework, language, image, command string }{
		{"jest", "javascript", "node:22-slim", "npm test"},
		{"rspec", "ruby", "ruby:3.2-slim", "bundle exec rspec"},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			root := jenkinsApplyRepository(t, tc.framework, jenkinsContainerStage("docker { image '"+tc.image+"' }", "", tc.command))
			require.Empty(t, setupImageNote(root, tc.language, tc.framework))
			text := providerSetupInstructions(root, jenkins{}, tc.language, tc.framework, "", true)
			require.Contains(t, text, "These jobs (Jenkinsfile / Test) run in "+tc.image+", which has neither curl nor wget, so Datadog's official installer cannot run there")
			require.Contains(t, text, fallbackComment[1]+"\nif [ \"${DD_API_KEY:+set}\" = set ]; then\n  "+strings.Join(fallbackBase, "\n  ")+"\n  export DD_TRACER_FOLDER=\""+strings.ReplaceAll(jenkinsTracerFolder, "$PWD", "$DD_TRACER_BASE")+"\"")
			require.Contains(t, text, `DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="jenkins"`)
			require.Contains(t, text, "add "+jenkinsTracerIgnore+" to the repository .gitignore")
			require.NotContains(t, text, "need another image")
		})
	}
	// An image without Bash gets it too, for every language.
	root := jenkinsApplyRepository(t, "jest", jenkinsContainerStage("docker { image 'node:22-alpine' }", "", "npm test"))
	require.Empty(t, setupImageNote(root, "javascript", "jest"))
	require.Contains(t, providerSetupInstructions(root, jenkins{}, "javascript", "jest", "", true), "These jobs (Jenkinsfile / Test) run in node:22-alpine, which has no Bash, so Datadog's official installer cannot run there")
}
