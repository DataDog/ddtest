// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jenkinsRepository is a Jest repository whose only CI is the Jenkinsfile;
// the test runs in it with Jenkins selected.
func jenkinsRepository(t *testing.T, jenkinsfile string) string {
	t.Helper()
	root := newJestRepository(t, jestWorkflow)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	writeFile(t, root, "Jenkinsfile", jenkinsfile)
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	return root
}

// jenkinsJobs reads the jobs of a Jenkinsfile.
func jenkinsJobs(t *testing.T, source string) map[string]runtimeJob {
	t.Helper()
	return jenkinsWorkflow(t, source).Jobs
}

func jenkinsWorkflow(t *testing.T, source string) ciWorkflow {
	t.Helper()
	workflow, err := readJenkinsfile(t.TempDir(), "Jenkinsfile", source)
	require.NoError(t, err)
	return workflow
}

// jenkinsRuns lists a job's commands, and the problems of unresolved steps.
func jenkinsRuns(job runtimeJob) []string {
	var runs []string
	for _, step := range job.Steps {
		switch {
		case step.Setup != nil:
			runs = append(runs, "SETUP")
		case step.ResolutionError != "":
			runs = append(runs, "UNRESOLVED "+step.ResolutionError)
		case step.Run != "":
			runs = append(runs, step.Run)
		}
	}
	return runs
}

const jenkinsDatadogOption = "  options {\n    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])\n  }\n"

func TestJenkinsConfigurationFiles(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"Jenkinsfile", "Jenkinsfile.release", "ci.Jenkinsfile", "jenkins/Jenkinsfile", ".ci/nightly.jenkinsfile", "docs/Jenkinsfile", "README.md", "jenkins/helper.groovy"} {
		writeFile(t, root, file, "node {}\n")
	}
	files, err := jenkins{}.configurationFiles(root)
	require.NoError(t, err)
	require.Equal(t, []string{".ci/nightly.jenkinsfile", "Jenkinsfile", "Jenkinsfile.release", "ci.Jenkinsfile", "jenkins/Jenkinsfile"}, files)
	workflows, err := jenkins{}.readWorkflows(root)
	require.NoError(t, err)
	require.Len(t, workflows, 5)
	require.Contains(t, workflows[0].Caveats[0], "Jenkins selects each job's Jenkinsfile (its Script Path) in the job configuration")
	empty, err := jenkins{}.configurationFiles(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, empty)
}

// Each leaf stage is a job; each top-level statement of an sh script one
// step, sharing that script's shell, and every sh a fresh shell.
func TestJenkinsDeclarativeStages(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent { label 'linux' }
  environment {
    CI = 'true'
    TOKEN = credentials('npm-token')
    REVISION = sh(returnStdout: true, script: 'git rev-parse HEAD').trim()
  }
  stages {
    stage('Install') { steps { sh 'npm ci' } }
    stage('Checks') {
      parallel {
        stage('Lint') { steps { sh 'npm run lint' } }
        stage('Unit') {
          environment { SUITE = 'unit' }
          steps {
            sh '''
              export FORCE_COLOR=0
              cd packages/app
              npm test -- --suite "$SUITE"
            '''
            sh 'npm run report'
          }
        }
      }
    }
    stage('Nested') {
      stages {
        stage('Inner') { steps { sh "echo ${env.CI}" } }
      }
    }
    stage('Never') {
      when { expression { return false } }
      steps { sh 'npm run never' }
    }
    stage('Maybe') {
      when { branch 'main' }
      steps { sh 'npm run maybe' }
    }
  }
}`)
	require.Equal(t, []string{"Checks / Lint", "Checks / Unit", "Install", "Maybe", "Nested / Inner", "Never"}, slices.Sorted(maps.Keys(jobs)))
	unit := jobs["Checks / Unit"]
	require.Equal(t, "the Jenkins agents labeled \"linux\"", unit.Host)
	require.Equal(t, "true", unit.Env["CI"])
	require.Equal(t, "unit", unit.Env["SUITE"])
	require.NoError(t, unresolvedCIError(unit.Env["CI"]))
	require.Equal(t, "${{ secrets.TOKEN }}", unit.Env["TOKEN"], "a credential is a secret only the build has")
	require.Error(t, unresolvedCIError(unit.Env["REVISION"]))
	require.Equal(t, []string{`npm test -- --suite "$SUITE"`, "npm run report"}, jenkinsRuns(unit))
	require.Equal(t, "packages/app", unit.Steps[2].WorkingDirectory, "cd carries within one sh script")
	require.Equal(t, "0", unit.Steps[2].Env["FORCE_COLOR"])
	require.Empty(t, unit.Steps[3].WorkingDirectory, "every sh starts a fresh shell")
	require.Empty(t, unit.Steps[3].Env["FORCE_COLOR"])
	require.Equal(t, []string{"echo true"}, jenkinsRuns(jobs["Nested / Inner"]), "a known environment value interpolates")
	require.Equal(t, "false", jobs["Never"].If)
	require.Empty(t, jobs["Maybe"].If, "a when ddtest cannot decide is a possible path")
}

func TestJenkinsMatrixAndPost(t *testing.T) {
	workflow := jenkinsWorkflow(t, `pipeline {
  agent any
  stages {
    stage('Test') {
      matrix {
        axes {
          axis { name 'NODE'; values '20', '22' }
          axis { name 'OS'; values 'linux', 'mac' }
        }
        excludes {
          exclude {
            axis { name 'NODE'; values '20' }
            axis { name 'OS'; notValues 'linux' }
          }
        }
        stages {
          stage('Run') { steps { sh 'npm test' } }
        }
      }
    }
    stage('Report') {
      steps { sh 'npm run coverage' }
      post {
        always { sh 'npm run upload' }
        failure { sh 'npm run diagnose' }
      }
    }
  }
  post {
    success { sh 'npm run notify' }
  }
}`)
	require.Equal(t, []string{"Report", "Test / Run (20, linux)", "Test / Run (22, linux)", "Test / Run (22, mac)", "post"}, slices.Sorted(maps.Keys(workflow.Jobs)))
	require.Equal(t, "22", workflow.Jobs["Test / Run (22, mac)"].Env["NODE"])
	require.Equal(t, "mac", workflow.Jobs["Test / Run (22, mac)"].Env["OS"])
	require.Equal(t, []string{"npm run coverage", "npm run upload"}, jenkinsRuns(workflow.Jobs["Report"]), "post conditions of a passing build run after the steps")
	require.Equal(t, []string{"npm run notify"}, jenkinsRuns(workflow.Jobs["post"]))
	require.Contains(t, strings.Join(workflow.Review, "\n"), "the post { failure } block at Jenkinsfile:25 runs only when the build does not pass")

	large := "pipeline {\n  agent any\n  stages {\n    stage('M') {\n      matrix {\n        axes {\n"
	for _, axis := range []string{"A", "B", "C"} {
		large += "          axis { name '" + axis + "'; values '1', '2', '3', '4', '5', '6' }\n"
	}
	large += "        }\n        stages { stage('t') { steps { sh 'npm test' } } }\n      }\n    }\n  }\n}\n"
	jobs := jenkinsJobs(t, large)
	require.Equal(t, []string{"UNRESOLVED Jenkinsfile M: ddtest cannot expand its matrix (it has more than 200 cells), so it cannot tell which stages it runs"}, jenkinsRuns(jobs["M"]))
}

func TestJenkinsAgentsAndContainers(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent none
  tools { nodejs 'NodeJS 22' }
  stages {
    stage('Docker') {
      agent { docker { image 'node:22-alpine' } }
      steps { sh 'npm test' }
    }
    stage('Dockerfile') {
      agent { dockerfile true }
      steps { sh 'npm test' }
    }
    stage('Kubernetes') {
      agent { kubernetes { yaml 'pod.yaml' } }
      steps { container('node') { sh 'npm test' } }
    }
    stage('Host') {
      agent { label 'linux' }
      steps { sh 'npm test' }
    }
    stage('Nowhere') {
      steps { sh 'npm test' }
    }
    stage('Windows') {
      agent { label 'windows' }
      steps { bat 'npm test' }
    }
  }
}`)
	docker := jobs["Docker"]
	require.Equal(t, "node:22-alpine", docker.Image)
	require.Contains(t, docker.SetupUnsupported, "it runs in agent { docker { image 'node:22-alpine' } }, a container: the Datadog Jenkins plugin installs the tracer on the build agent and does not instrument tests inside containers")
	require.Contains(t, jobs["Dockerfile"].ImageProblem, "agent { dockerfile … }")
	require.NotEmpty(t, jobs["Kubernetes"].SetupUnsupported)
	require.Empty(t, jobs["Host"].SetupUnsupported)
	require.Equal(t, "the Jenkins agents labeled \"linux\", with the Jenkins tool nodejs 'NodeJS 22' (tools directive) on PATH", jobs["Host"].Host)
	require.Equal(t, []string{"UNRESOLVED Jenkinsfile Nowhere: it runs under agent none, without an agent for shell steps"}, jenkinsRuns(jobs["Nowhere"]))
	require.Equal(t, []string{"UNRESOLVED Jenkinsfile Windows: runs bat, a Windows command; ddtest does not set up or validate Windows agents"}, jenkinsRuns(jobs["Windows"]))
}

// Anything ddtest cannot read statically is an unresolved step, never
// dropped: computed scripts, unknown steps (shared libraries), Groovy
// functions, load, build and tests started in containers by sh.
func TestJenkinsUnresolvedSteps(t *testing.T) {
	jobs := jenkinsJobs(t, `@Library('ci-lib') _
def runTests(target) { sh "npm test ${target}" }
pipeline {
  agent any
  parameters { string(name: 'SUITE', defaultValue: 'unit') }
  stages {
    stage('Computed') { steps { sh "npm test ${env.SHARD}" } }
    stage('Parameter') { steps { sh "npm test -- ${params.SUITE}" } }
    stage('Library') { steps { nodePipeline(node: 22) } }
    stage('Wrapper') { steps { withNpmEnv { sh 'npm test' } } }
    stage('Function') { steps { script { runTests('unit') } } }
    stage('Load') { steps { load 'ci/steps.groovy' } }
    stage('Build') { steps { build job: 'downstream' } }
    stage('Docker run') { steps { sh 'docker run --rm -v "$PWD:/app" node:22 npm test' } }
    stage('Compose') { steps { sh 'docker-compose -f ci.yml run tests' } }
    stage('Shebang') { steps { sh '''#!/usr/bin/env python3
print("x")
''' } }
    stage('Literal') { steps { sh 'npm test' } }
  }
}`)
	unresolved := func(name string) string {
		runs := jenkinsRuns(jobs[name])
		require.Len(t, runs, 1, name)
		return runs[0]
	}
	require.Contains(t, unresolved("Computed"), "its sh script reads ${env.SHARD}, which only the Jenkins build knows")
	require.Equal(t, "npm test -- unit", unresolved("Parameter"), "a parameter's default is known")
	require.Contains(t, unresolved("Library"), "it calls nodePipeline, which is not a Pipeline step ddtest knows; it may come from the shared library ci-lib")
	require.Contains(t, unresolved("Wrapper"), "it runs inside withNpmEnv(…), which is not a Pipeline step ddtest knows")
	require.Contains(t, unresolved("Function"), "it calls runTests(), a Groovy function of the Jenkinsfile")
	require.Contains(t, unresolved("Load"), "load runs Groovy code from 'ci/steps.groovy'")
	require.Contains(t, unresolved("Build"), "build triggers another Jenkins job")
	require.Contains(t, unresolved("Docker run"), "it runs docker run: tests inside run in a container ddtest cannot validate, and the Datadog Jenkins plugin does not instrument them")
	require.Contains(t, unresolved("Compose"), "it runs docker-compose run")
	require.Contains(t, unresolved("Shebang"), "its sh script runs with /usr/bin/env python3, not a shell ddtest reads")
	require.Equal(t, "npm test", unresolved("Literal"))
	// The step keeps the command, so ddtest can tell whether it may run tests.
	require.Equal(t, `docker run --rm -v "$PWD:/app" node:22 npm test`, jobs["Docker run"].Steps[0].Run)
}

func TestJenkinsScriptedPipelines(t *testing.T) {
	workflow := jenkinsWorkflow(t, `node('linux') {
  checkout scm
  stage('Build') {
    sh 'npm ci'
  }
  stage('Test') {
    dir('web') {
      withEnv(['NODE_ENV=test', 'PATH+NODE=/opt/node/bin']) {
        sh 'npm test'
      }
    }
    docker.image('node:22').inside {
      sh 'npm run e2e'
    }
  }
  parallel(
    unit: { sh 'npm run unit' },
    lint: { sh 'npm run lint' },
    failFast: true
  )
  if (env.BRANCH_NAME == 'main') {
    sh 'npm run deploy'
  }
  try {
    sh 'npm run flaky'
  } catch (err) {
    sh 'npm run on-failure'
  }
  env.SUITE = 'all'
  sh "npm run ${env.SUITE}"
}
stage('Outside') {
  node {
    sh 'npm run outside'
  }
}`)
	jobs := workflow.Jobs
	require.Equal(t, []string{"Build", "Outside", "Test", "lint", "node (linux)", "unit"}, slices.Sorted(maps.Keys(jobs)))
	test := jobs["Test"]
	require.Equal(t, []string{"npm test", "npm run e2e"}, jenkinsRuns(test))
	require.Equal(t, "web", test.Steps[0].WorkingDirectory)
	require.Equal(t, "test", test.Steps[0].Env["NODE_ENV"])
	require.Contains(t, test.SetupUnsupported, "docker.image('node:22').inside")
	require.Equal(t, []string{"npm run deploy", "npm run flaky", "npm run all"}, jenkinsRuns(jobs["node (linux)"]), "a catch block is not a passing path")
	require.Equal(t, "the Jenkins agents labeled \"linux\"", jobs["node (linux)"].Host)
	require.Equal(t, "any Jenkins agent (node, with no label)", jobs["Outside"].Host)
	require.Equal(t, []string{"checkout"}, jenkinsNonTest(workflow))
}

func jenkinsNonTest(workflow ciWorkflow) []string {
	if len(workflow.Caveats) == 0 {
		return nil
	}
	_, list, _ := strings.Cut(workflow.Caveats[0], "outside this validation: ")
	return strings.Split(strings.TrimSuffix(list, "."), ", ")
}

func TestJenkinsRecognizesThePluginSetup(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent { label 'linux' }
  options {
    timeout(time: 1, unit: 'HOURS')
    datadog(collectLogs: true, testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT', 'RUBY', 'GO'], additionalVariables: [DD_SITE: 'datadoghq.eu', NODE_OPTIONS: '--x']])
  }
  stages {
    stage('Test') {
      environment { NODE_OPTIONS = '--max-old-space-size=4096' }
      steps { sh 'npm test' }
    }
    stage('Tools') {
      tools { nodejs '22' }
      steps { sh 'npm test' }
    }
  }
}`)
	test := jobs["Test"]
	require.Equal(t, []string{"SETUP", "npm test"}, jenkinsRuns(test))
	setup := test.Steps[0].Setup
	require.Equal(t, jenkinsPluginReference, setup.Reference)
	require.Equal(t, "js ruby", setup.Inputs["languages"])
	require.Equal(t, "datadoghq.eu", setup.Inputs["site"])
	require.True(t, setup.Floating)
	require.True(t, setup.WorkspaceRoot)
	require.Empty(t, setup.Problem)
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact}, setup.Confirmations)
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }}", test.Steps[1].Env["NODE_OPTIONS"], "the plugin sets NODE_OPTIONS once per build, over a stage's")
	require.Equal(t, "-rbundler/setup -rdatadog/ci/auto_instrument", test.Steps[1].Env["RUBYOPT"])
	selected, err := setupSelectsLanguage(*setup, nil, "js")
	require.NoError(t, err)
	require.True(t, selected)
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact, jenkinsRuntimeMatches}, jobs["Tools"].Steps[0].Setup.Confirmations, "a PATH change needs the agent runtime confirmed")

	pipelineOptions := jenkinsJobs(t, "pipeline {\n  agent any\n  environment { NODE_OPTIONS = '--max-old-space-size=4096' }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }} --max-old-space-size=4096", pipelineOptions["T"].Steps[1].Env["NODE_OPTIONS"], "the plugin prepends its preload to the pipeline's options")
	workflow := jenkinsWorkflow(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages {\n    stage('T') {\n      steps {\n        withEnv(['NODE_OPTIONS=--inspect']) { sh 'npm test' }\n      }\n    }\n  }\n}\n")
	require.Contains(t, strings.Join(workflow.Review, "\n"), "NODE_OPTIONS set in withEnv at Jenkinsfile:9 is overwritten by the Datadog Jenkins plugin")
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }}", workflow.Jobs["T"].Steps[1].Env["NODE_OPTIONS"])

	scripted := jenkinsJobs(t, "datadog(testVisibility: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {\n  node {\n    stage('Test') { sh 'bundle exec rspec' }\n  }\n}\nnode { stage('Other') { sh 'bundle exec rspec' } }\n")
	require.Equal(t, "ruby", scripted["Test"].Steps[0].Setup.Inputs["languages"], "testVisibility is the option's legacy name")
	require.Equal(t, []string{"bundle exec rspec"}, jenkinsRuns(scripted["Other"]), "the wrap applies only to the nodes inside it")
}

func TestJenkinsSetupProblems(t *testing.T) {
	for _, tc := range []struct{ name, option, problem string }{
		{name: "computed", option: "datadog(testOptimization: settings)", problem: "is not a literal map"},
		{name: "disabled", option: "datadog(testOptimization: [enabled: false, serviceName: 'a', languages: ['JAVASCRIPT']])", problem: "does not set enabled: true literally"},
		{name: "no languages", option: "datadog(testOptimization: [enabled: true, serviceName: 'a'])", problem: "has no literal languages list"},
		{name: "no service", option: "datadog(testOptimization: [enabled: true, languages: ['JAVASCRIPT']])", problem: "without one the plugin reports the tests under service null"},
		{name: "key", option: "datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT'], additionalVariables: [DD_API_KEY: 'abc']])", problem: "sets DD_API_KEY in additionalVariables, which writes the key into the Jenkinsfile"},
		{name: "opt out", option: "datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT'], additionalVariables: [DD_CIVISIBILITY_ENABLED: 'false']])", problem: "disables Test Optimization with DD_CIVISIBILITY_ENABLED=false"},
		{name: "computed variable", option: "datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT'], additionalVariables: [DD_ENV: env.X]])", problem: "sets DD_ENV in additionalVariables to env.X, which ddtest cannot resolve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := jenkinsJobs(t, "pipeline {\n  agent any\n  options { "+tc.option+" }\n  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
			require.Contains(t, jobs["T"].Steps[0].Setup.Problem, tc.problem)
			require.Contains(t, jenkins{}.setupProblem(*jobs["T"].Steps[0].Setup, "js-tracer-version", exactRelease, "", ""), tc.problem)
		})
	}
	jobs := jenkinsJobs(t, "pipeline {\n  agent any\n  environment { DD_API_KEY = 'abc' }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Contains(t, jobs["T"].Steps[0].Setup.Problem, "DD_API_KEY is written into the Jenkinsfile (the environment directive at Jenkinsfile:3)")
	workflow := jenkinsWorkflow(t, "pipeline {\n  agent any\n  environment { DD_API_KEY = credentials('dd') }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Empty(t, workflow.Jobs["T"].Steps[0].Setup.Problem)
	require.Contains(t, workflow.Review[0], "binds DD_API_KEY from a Jenkins credential; the Datadog Jenkins plugin overwrites DD_API_KEY")
	jobs = jenkinsJobs(t, "pipeline {\n  agent any\n  stages {\n    stage('T') {\n      options { datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT']]) }\n      steps { sh 'npm test' }\n    }\n  }\n}\n")
	require.Contains(t, jobs["T"].Steps[0].Setup.Problem, "is in a stage's options; ddtest certifies the Datadog Jenkins plugin setup only in the pipeline's top-level options")
	jobs = jenkinsJobs(t, "pipeline {\n  agent { docker { image 'node:22' } }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Contains(t, jobs["T"].Steps[0].Setup.Problem, "This stage cannot use the Datadog Jenkins plugin: it runs in agent { docker { image 'node:22' } }")
}

// The plugin installs the latest tracer: setupProblem accepts no release,
// but a release a language reads from the project must be exact, and the
// site is the one additionalVariables or the confirmed mode names.
func TestJenkinsSetupProblemTracerAndSite(t *testing.T) {
	setup := (&jenkinsSetup{languages: []string{"JAVASCRIPT"}, service: "a"}).ciSetup()
	require.Empty(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.com"))
	pinned := setup
	pinned.Inputs = maps.Clone(setup.Inputs)
	pinned.Inputs["ruby-tracer-version"] = ""
	require.Contains(t, jenkins{}.setupProblem(pinned, "ruby-tracer-version", exactRubyRelease.MatchString, "1.42.0", ""), "is not an exact tracer release")
	pinned.Inputs["ruby-tracer-version"] = "1.42.0"
	require.Empty(t, jenkins{}.setupProblem(pinned, "ruby-tracer-version", exactRubyRelease.MatchString, "1.42.0", ""))

	useCIProvider(t, "jenkins")
	useCIStatements(t, nil, "jenkins-plugin-mode=agentless:datadoghq.eu")
	require.Contains(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.com"), "The Datadog Jenkins plugin reports to datadoghq.eu (--ci-confirm jenkins-plugin-mode=agentless:datadoghq.eu), but the selected Datadog site is datadoghq.com")
	require.Empty(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.eu"))
	useCIStatements(t, nil, "jenkins-plugin-mode=agent")
	require.Empty(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.com"), "in Agent mode the Agent selects the site")
	site := setup
	site.Inputs = maps.Clone(setup.Inputs)
	site.Inputs["site"] = "us5.datadoghq.com"
	require.Contains(t, jenkins{}.setupProblem(site, "js-tracer-version", exactRelease, "", "datadoghq.com"), "reports to us5.datadoghq.com (DD_SITE in its additionalVariables)")
}

func TestJenkinsFacts(t *testing.T) {
	useCIProvider(t, "jenkins")
	for _, tc := range []struct {
		facts []string
		err   string
	}{
		{facts: []string{"jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com", "jenkins-agent-runtime-matches"}},
		{facts: []string{"jenkins-plugin-mode=agent"}},
		{facts: []string{"jenkins-plugin-mode=agentless"}, err: "--ci-confirm jenkins-plugin-mode needs a value: jenkins-plugin-mode=agentless:<site>|agent"},
		{facts: []string{"jenkins-plugin-mode=agentless:not a site"}, err: "needs a value"},
		{facts: []string{"jenkins-plugin=yes"}, err: "--ci-confirm jenkins-plugin takes no value"},
		{facts: []string{"plugin"}, err: "--ci-confirm plugin is not a Jenkins fact; facts: jenkins-plugin, jenkins-plugin-mode, jenkins-agent-runtime-matches"},
	} {
		var facts []CIFact
		for _, value := range tc.facts {
			fact, err := ParseCIConfirm(value)
			require.NoError(t, err)
			facts = append(facts, fact)
		}
		err := UseCIStatements(nil, facts)
		if tc.err != "" {
			require.ErrorContains(t, err, tc.err, tc.facts)
			continue
		}
		require.NoError(t, err, tc.facts)
	}
	useCIStatements(t, nil, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com")
	stated, _ := StatedFacts(nil, "javascript")
	require.Equal(t, []string{
		"The Datadog Jenkins plugin 5.6.2 or newer is installed on the Jenkins controller (--ci-confirm)",
		"The Datadog Jenkins plugin is configured in Agentless mode for datadoghq.com, with the API key as a Jenkins credential (--ci-confirm)",
	}, stated)
}

// With the plugin in place, ddtest asks for the facts only Jenkins shows
// before it certifies anything, and only for jobs that run the tests.
func TestJenkinsAsksToConfirmThePlugin(t *testing.T) {
	root := jenkinsRepository(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages {\n    stage('Lint') { steps { sh 'echo lint' } }\n    stage('Test') { tools { nodejs '22' }\n      steps { sh 'npm test' } }\n  }\n}\n")
	required := RequiredCIConfirmations(root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{"Jenkinsfile / Test"}, required.Jobs)
	var keys []string
	for _, fact := range required.Facts {
		keys = append(keys, fact.Key)
	}
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact, jenkinsRuntimeMatches}, keys)
	result := CIConfirmationRequiredResult(required)
	require.Equal(t, "needs_input", result.Status)
	require.Equal(t, "ci_setup_confirmation_required", result.Code)
	require.Equal(t, "ask_user", result.NextAction)
	require.Equal(t, []string{"ci_confirm:jenkins-plugin", "ci_confirm:jenkins-plugin-mode=agentless:<site>|agent", "ci_confirm:jenkins-agent-runtime-matches"}, result.RequiredInput)
	require.Contains(t, result.Instruction, "jenkins-plugin: Is the Datadog plugin 5.6.2 or newer installed on the Jenkins controller?")
	require.Contains(t, result.Instruction, "rerun the same command adding --ci-confirm jenkins-plugin --ci-confirm jenkins-plugin-mode=agentless:<site>|agent --ci-confirm jenkins-agent-runtime-matches")

	useCIStatements(t, nil, "jenkins-plugin", "jenkins-plugin-mode=agent")
	required = RequiredCIConfirmations(root, "javascript", "jest")
	require.Len(t, required.Facts, 1)
	useCIStatements(t, nil, "jenkins-plugin", "jenkins-plugin-mode=agent", "jenkins-agent-runtime-matches")
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))

	// Without the setup, or with a broken one, nothing is asked yet.
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	require.NoError(t, UseCIStatements(nil, nil))
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  options { datadog(testOptimization: [enabled: true, languages: ['JAVASCRIPT']]) }\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))
	require.Contains(t, strings.Join(existingSetupProblems(root, "javascript", "jest", ""), "\n"), "Jenkinsfile / Test: The datadog(testOptimization: …) option at Jenkinsfile:3 has no literal serviceName")
}

func TestJenkinsLanguageSupport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Jenkinsfile", "node { sh 'pytest' }\n")
	useCIProvider(t, "jenkins")
	for language, supported := range map[string]bool{"javascript": true, "ruby": true, "python": false, "java": false} {
		err := CheckCIProviderSupport(root, language, "framework")
		if supported {
			require.NoError(t, err, language)
			continue
		}
		var unsupported *CIProviderUnsupportedError
		require.ErrorAs(t, err, &unsupported, language)
		require.Equal(t, "Jenkins", unsupported.Provider)
		require.Equal(t, "ci_provider_unsupported", ProviderUnsupportedResult(unsupported).Code)
	}
}

// Every job running the tests in a container: the plugin cannot instrument
// any, so ddtest reports the repository unsupported instead of prescribing.
func TestJenkinsContainerTestJobsAreUnsupported(t *testing.T) {
	root := jenkinsRepository(t, "pipeline {\n  agent { docker { image 'node:22' } }\n  stages {\n    stage('Test') { steps { sh 'npm test' } }\n  }\n}\n")
	err := CheckCIProviderSupport(root, "javascript", "jest")
	var unsupported *CIProviderUnsupportedError
	require.ErrorAs(t, err, &unsupported)
	result := ProviderUnsupportedResult(unsupported)
	require.Equal(t, "unsupported", result.Status)
	require.Equal(t, "ci_setup_unsupported", result.Code)
	require.Contains(t, result.Reason, "every CI job that runs the tests is outside what the Datadog Jenkins plugin instruments. Jenkinsfile / Test: it runs in agent { docker { image 'node:22' } }")
	require.Contains(t, result.Reason, "The user's options are to run those tests where the Datadog Jenkins plugin instruments them, or to set up Test Optimization for them by hand, outside ddtest, which ddtest cannot validate.")
	require.Contains(t, result.Instruction, "Report this result to the user with its reason and stop. Do not move tests between agents, edit CI, add a tracer or set up Test Optimization by hand")

	var out bytes.Buffer
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	var started AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &started), out.String())
	require.Equal(t, "ci_setup_unsupported", started.Code)

	// One host job is enough to set up; the container job stays reported.
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent none\n  stages {\n    stage('Docker') { agent { docker { image 'node:22' } }\n      steps { sh 'npm test' } }\n    stage('Host') { agent any\n      steps { sh 'npm test' } }\n  }\n}\n")
	require.NoError(t, CheckCIProviderSupport(root, "javascript", "jest"))
	request := javascriptDiscovery{}.ciSetupRequest(root, "jest", "datadoghq.com", jenkins{}.setupTerms())
	require.Contains(t, strings.Join(request.Notes, "\n"), "The Datadog Jenkins plugin cannot instrument these CI jobs, so ddtest reports them as unsupported and does not validate them; tell the user, and do not move their tests to other agents without the user's approval:\nJenkinsfile / Docker: it runs in agent { docker { image 'node:22' } }")
}

func TestJenkinsProviderChoice(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	UseCIProvider("")
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.Equal(t, []string{"github", "jenkins"}, choice.Providers)
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages { stage('Deploy') { steps { sh 'echo deploy' } } }\n}\n")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "github", selected)
}

func TestJenkinsInstructions(t *testing.T) {
	root := jenkinsRepository(t, "pipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	text := providerSetupInstructions(root, jenkins{}, "javascript", "vitest", "datadoghq.eu", true)
	require.Contains(t, text, "datadog(testOptimization: [enabled: true, serviceName: '<service>', languages: ['JAVASCRIPT']])")
	require.Contains(t, text, "Agentless mode with the Datadog API key stored as a Jenkins credential and the site datadoghq.eu")
	require.Contains(t, text, "add this line before the test command (inside the same sh string, so it runs in the test's shell):\n\n```sh\n"+pluginESMImportCommand+"\n```")
	require.Contains(t, text, "installs the latest dd-trace release with the build agent's default npm when each build runs (it cannot pin a release)")
	require.NotContains(t, text, "Try it locally", "agents run the coordinator")
	require.NotContains(t, text, "ddtest onboard --framework vitest --apply", "onboard --apply does not edit Jenkinsfiles")

	ruby := rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", jenkins{}.setupTerms())
	notes := strings.Join(ruby.Notes, "\n")
	require.Contains(t, notes, `Pin datadog-ci in the bundle: add gem "datadog-ci", "<exact datadog-ci release>", group: :test to the Gemfile at the checkout root`)
	require.Contains(t, notes, "keep the Gemfile at the checkout root and run the rspec tests from there: not inside dir(…), and without BUNDLE_GEMFILE")
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.40.0)\n")
	ruby = rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", jenkins{}.setupTerms())
	require.Contains(t, ruby.Notes[0], "already pins datadog-ci 1.40.0")

	require.False(t, providerAutomaticSetup(jenkins{}, javascriptDiscovery{}, "jest"), "Jenkins setup is applied from instructions")
	require.True(t, providerAutomaticSetup(buildkite{}, javascriptDiscovery{}, "jest"))
	_, err := planProviderJavaScriptSetup(root, jenkins{}, nil, "jest", "5.0.0", "datadoghq.com", "")
	require.ErrorContains(t, err, "onboard --apply does not edit the Jenkins configuration")
}

// The committed fixture is ral-facilities/scigateway's declarative
// Jenkinsfile: Jest in the Unit Tests stage of a labeled host agent, and an
// E2E stage outside the Jest path.
func TestJenkinsJavaScriptFixture(t *testing.T) {
	root := copyFixture(t, "jenkins-javascript")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "jest", scope.Commands[0].Command)
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{"Jenkinsfile / Unit Tests"}, required.Jobs)
	require.Equal(t, []string{"the Jenkins agents labeled \"sl7\""}, required.Hosts)
	require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))

	jenkinsfile, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	writeFile(t, root, "Jenkinsfile", strings.Replace(string(jenkinsfile), "    agent { label 'sl7'}\n", "    agent { label 'sl7'}\n"+jenkinsDatadogOption, 1))
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
	useCIStatements(t, []string{"node=22"}, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com")
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))
	result := checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, action, version string) (tracerRequirement, error) {
		require.Equal(t, jenkinsPluginReference, action)
		require.Equal(t, "latest", version, "the plugin installs the latest dd-trace")
		return tracerRequirement{Version: "6.22.0", Node: ">=18", Requested: "latest"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, result.Status, result)
	var unit RuntimeFinding
	for _, finding := range result.Jobs {
		if finding.Job == "Unit Tests" {
			unit = finding
		}
	}
	require.True(t, unit.UserStated)
	require.True(t, unit.TracerFloating)
	require.Equal(t, "dd-trace@6.22.0", unit.Tracer)
	caveats := CISetupCaveats(&result)
	require.Len(t, caveats, 2)
	require.Contains(t, caveats[1], "dd-trace version: the Datadog Jenkins plugin installs the latest dd-trace release when each build runs and cannot pin one; ddtest checked the CI runtime against, and validated locally with, dd-trace@6.22.0")
}

// An ESM framework needs the plugin's loader in the test's sh script.
func TestJenkinsESMLoader(t *testing.T) {
	check := func(script string) RuntimeCheck {
		root := jenkinsRepository(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages { stage('Test') { steps { sh '''"+script+"''' } } }\n}\n")
		writeFile(t, root, "package.json", `{"scripts": {"test": "vitest run"}, "devDependencies": {"vitest": "3.2.4"}}`)
		useCIStatements(t, []string{"node=22"})
		return checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.22.0", Node: ">=18"}, nil
		}, "vitest")
	}
	require.NotEqual(t, StatusCompatible, check("npm test").Status, "without the loader, ESM tests are not traced")
	result := check(pluginESMImportCommand + "\nnpm test")
	require.Equal(t, StatusCompatible, result.Status, result)
	require.Equal(t, "--max-old-space-size=4096", mustNodeOptions(t, "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_PATH }}/register.js --max-old-space-size=4096"), "local replay drops the plugin's loader")
}

func mustNodeOptions(t *testing.T, value string) string {
	t.Helper()
	resolved, err := resolveNodeOptions(value, nil, nil)
	require.NoError(t, err)
	return resolved
}

// The committed fixture is deanwilson/puppet-multitemplate's scripted
// Jenkinsfile: bundle exec rake spec in the Test stage of a node block.
func TestJenkinsRubyFixture(t *testing.T) {
	root := copyFixture(t, "jenkins-ruby")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "bundle exec rake spec", scope.Commands[0].Command)
	require.Equal(t, []string{"Jenkinsfile / Test"}, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec").Jobs)

	jenkinsfile, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	wrapped := strings.Replace(string(jenkinsfile), "node {", "datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {\nnode {", 1) + "}\n"
	writeFile(t, root, "Jenkinsfile", wrapped)
	useCIStatements(t, []string{"ruby=3.3"}, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com")
	finding := func() RuntimeFinding {
		for _, job := range RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs {
			if job.Job == "Test" {
				return job
			}
		}
		t.Fatal("no Test finding")
		return RuntimeFinding{}
	}
	unpinned := finding()
	require.Equal(t, CodeIncompleteCISetup, unpinned.Code)
	require.Contains(t, unpinned.Reason, "runs bundle add datadog-ci, which unfreezes the bundle and installs the latest datadog-ci release")
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))

	lock, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	writeFile(t, root, "Gemfile.lock", strings.Replace(string(lock), "    diff-lcs (1.6.2)\n", "    datadog-ci (1.42.0)\n    diff-lcs (1.6.2)\n", 1))
	pinned := finding()
	require.Equal(t, "not checked", pinned.Status, pinned.Reason)
	require.Equal(t, "1.42.0", pinned.TracerRequested)
	require.Equal(t, "3.3", pinned.Runtime)
	require.Contains(t, pinned.SetupCaveats[1], "uses the datadog-ci release the repository's Gemfile.lock pins (1.42.0)")
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))

	writeFile(t, root, "Jenkinsfile", strings.Replace(wrapped, "sh 'bundle exec rake spec'", "dir('sub') { sh 'bundle exec rake spec' }", 1))
	require.Contains(t, finding().Reason, "adds datadog-ci from the checkout root's bundle, but this test command runs in sub")
	writeFile(t, root, "Jenkinsfile", strings.Replace(wrapped, "sh 'bundle exec rake spec'", "withEnv(['BUNDLE_GEMFILE=gemfiles/rails.gemfile']) { sh 'bundle exec rake spec' }", 1))
	require.Contains(t, finding().Reason, "selects BUNDLE_GEMFILE=gemfiles/rails.gemfile")
}

// ${WORKSPACE} is the checkout: in an environment value, the shared model's
// expression for it; in a script, the shell's $WORKSPACE.
func TestJenkinsWorkspace(t *testing.T) {
	jobs := jenkinsJobs(t, "pipeline {\n  agent any\n  environment {\n    GEM_DIR = \"${WORKSPACE}/.gem\"\n    npm_config_cache = \"${WORKSPACE}/.npm\"\n    PATH = \"${WORKSPACE}/node_modules/.bin:${env.PATH}\"\n  }\n  stages { stage('T') { steps {\n    dir(\"${WORKSPACE}/web\") { sh \"cd ${env.WORKSPACE}/web; npm test\" }\n  } } }\n}\n")
	job := jobs["T"]
	require.Equal(t, ciProjectDirectory+"/.gem", job.Env["GEM_DIR"])
	require.NotContains(t, job.Env, "npm_config_cache", "an install directory in the workspace is the agent's, not the tests'")
	require.NotContains(t, job.Env, "PATH")
	require.Equal(t, "any Jenkins agent (agent any, with no label)", job.Host, "the checkout's node_modules/.bin is no toolchain change")
	require.Equal(t, "web", job.Steps[0].WorkingDirectory)
	require.Equal(t, []string{"npm test"}, jenkinsRuns(job))
	require.Equal(t, "web", job.Steps[1].WorkingDirectory)
}

// A coding agent that applies the setup by hand gets the validation scope,
// unresolved entries included, even for frameworks whose automatic setup
// prints none.
func TestJenkinsAgentInstructionsListTheScope(t *testing.T) {
	jenkinsRepository(t, "@Library('lib') _\npipeline {\n  agent any\n  stages {\n    stage('Test') { steps { sh 'npm test' } }\n    stage('Publish') { steps { publishImage() } }\n  }\n}\n")
	var guidance bytes.Buffer
	require.NoError(t, run(&guidance, &agentHandoff{}))
	require.Contains(t, guidance.String(), "jest — Jenkinsfile / Test / step 1 (npm test)")
	require.Contains(t, guidance.String(), "Unresolved scope: Jenkinsfile / Publish / step 1 (): Jenkinsfile Publish: it calls publishImage, which is not a Pipeline step ddtest knows; it may come from the shared library lib")
}
